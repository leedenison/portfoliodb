import { describe, expect, it } from "vitest";
import { originPattern } from "./permissions";

describe("originPattern", () => {
  it("covers every path on the origin", () => {
    expect(originPattern("http://localhost:8080")).toBe("http://localhost:8080/*");
    expect(originPattern("https://portfolio.example.com")).toBe("https://portfolio.example.com/*");
  });

  it("does not double the separator when the origin has a trailing slash", () => {
    // A pattern like "http://host//*" matches nothing, so a stored origin that
    // kept its slash would silently fail every permission check.
    expect(originPattern("http://localhost:8080/")).toBe("http://localhost:8080/*");
    expect(originPattern("http://localhost:8080///")).toBe("http://localhost:8080/*");
  });

  it("builds a wildcard-subdomain broker pattern unchanged", () => {
    expect(originPattern("https://*.fidelity.co.uk")).toBe("https://*.fidelity.co.uk/*");
  });
});
