/**
 * Host permissions for origins that are not known until runtime: the
 * user-configured PortfolioDB deployment, and each broker its recipe names.
 *
 * Neither can be declared in the manifest -- the first is arbitrary, and naming
 * the second would mean editing the manifest to add a broker. Both are granted
 * from optional_host_permissions instead.
 */

/** Match pattern covering every path on an origin. */
export function originPattern(origin: string): string {
  return `${origin.replace(/\/+$/, "")}/*`;
}

export function hasOriginPermission(origin: string): Promise<boolean> {
  return chrome.permissions.contains({ origins: [originPattern(origin)] });
}

/**
 * Prompts for access to an origin.
 *
 * Must be called from a user gesture, which means the popup: a service worker has
 * no gesture, and handling a message sent from a popup click does not inherit
 * one, so requesting from the worker fails.
 */
export function requestOriginPermission(origin: string): Promise<boolean> {
  return chrome.permissions.request({ origins: [originPattern(origin)] });
}

/**
 * Variants taking match patterns directly, for recipes: a recipe declares the
 * patterns it needs, which may cover more than the single origin its home page
 * sits on.
 */
export function hasPatternPermission(patterns: string[]): Promise<boolean> {
  return chrome.permissions.contains({ origins: patterns });
}

/** Must be called from a user gesture; see requestOriginPermission. */
export function requestPatternPermission(patterns: string[]): Promise<boolean> {
  return chrome.permissions.request({ origins: patterns });
}
