/**
 * OFX SGML parser. Converts OFX 1.x SGML text into a nested JS object.
 *
 * OFX SGML quirks handled:
 * - Leaf values have no closing tag: <UNITS>100
 * - Container elements use closing tags: <INVBUY>...</INVBUY>
 * - Repeated sibling tags (e.g. multiple <BUYSTOCK>) become arrays.
 * - Header block (key:value pairs before <OFX>) is parsed separately.
 */

export interface OfxDocument {
  header: Record<string, string>;
  body: Record<string, unknown>;
}

/** Parse the key:value header lines before <OFX>. */
function parseHeader(headerText: string): Record<string, string> {
  const result: Record<string, string> = {};
  for (const line of headerText.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const idx = trimmed.indexOf(":");
    if (idx < 0) continue;
    result[trimmed.slice(0, idx)] = trimmed.slice(idx + 1);
  }
  return result;
}

/**
 * Tokenize SGML body into open-tag, close-tag, and text tokens.
 * Returns array of { type, name?, value? }.
 */
interface Token {
  type: "open" | "close" | "text";
  name?: string;
  value?: string;
}

function tokenize(sgml: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < sgml.length) {
    if (sgml[i] === "<") {
      const end = sgml.indexOf(">", i);
      if (end < 0) break;
      const tag = sgml.slice(i + 1, end).trim();
      if (tag.startsWith("/")) {
        tokens.push({ type: "close", name: tag.slice(1).trim() });
      } else {
        tokens.push({ type: "open", name: tag });
      }
      i = end + 1;
    } else {
      const next = sgml.indexOf("<", i);
      const text = (next < 0 ? sgml.slice(i) : sgml.slice(i, next)).trim();
      if (text) {
        tokens.push({ type: "text", value: text });
      }
      i = next < 0 ? sgml.length : next;
    }
  }
  return tokens;
}

/** Add a value to a parent object, converting to array on duplicate keys. */
function addChild(
  parent: Record<string, unknown>,
  key: string,
  value: unknown,
): void {
  if (key in parent) {
    const existing = parent[key];
    if (Array.isArray(existing)) {
      existing.push(value);
    } else {
      parent[key] = [existing, value];
    }
  } else {
    parent[key] = value;
  }
}

/**
 * Recursively parse tokens starting at `pos` into the given parent object.
 * Returns the index after the last consumed token.
 */
function parseChildren(
  tokens: Token[],
  pos: number,
  parent: Record<string, unknown>,
  parentTag?: string,
): number {
  let i = pos;
  while (i < tokens.length) {
    const tok = tokens[i];

    if (tok.type === "close") {
      // Closing tag for our parent -- stop.
      if (parentTag && tok.name === parentTag) return i + 1;
      // Stray close tag -- skip.
      i++;
      continue;
    }

    if (tok.type === "text") {
      // Stray text outside a tag -- ignore.
      i++;
      continue;
    }

    // Open tag.
    const tagName = tok.name!;
    const next = tokens[i + 1];

    if (next && next.type === "text") {
      // Leaf: <TAG>value  (optionally followed by </TAG>)
      addChild(parent, tagName, next.value!);
      i += 2;
      // Consume optional closing tag.
      if (i < tokens.length && tokens[i].type === "close" && tokens[i].name === tagName) {
        i++;
      }
    } else if (next && next.type === "close" && next.name === tagName) {
      // Empty element: <TAG></TAG>
      addChild(parent, tagName, "");
      i += 2;
    } else {
      // Container element: recurse into children.
      const child: Record<string, unknown> = {};
      i = parseChildren(tokens, i + 1, child, tagName);
      addChild(parent, tagName, child);
    }
  }
  return i;
}

/** Parse an OFX SGML document into header + body. */
export function parseOfxSgml(text: string): OfxDocument {
  const ofxStart = text.indexOf("<OFX>");
  const headerText = ofxStart > 0 ? text.slice(0, ofxStart) : "";
  const sgml = ofxStart >= 0 ? text.slice(ofxStart) : text;

  const header = parseHeader(headerText);
  const tokens = tokenize(sgml);
  const body: Record<string, unknown> = {};
  parseChildren(tokens, 0, body);

  return { header, body };
}
