import { describe, expect, it } from "vitest";
import { Broker } from "@/gen/api/v1/api_pb";
import { getRecipe, getRecipeForBroker, listRecipes, renderExport, sourceFor } from "./index";

describe("recipe registry", () => {
  it("registers Fidelity UK against the Fidelity broker", () => {
    const recipe = getRecipeForBroker(Broker.FIDELITY);
    expect(recipe?.id).toBe("fidelity-uk");
    expect(getRecipe("fidelity-uk")).toBe(recipe);
    expect(listRecipes()).toHaveLength(1);
  });

  it("reuses the web client's source string rather than minting a new one", () => {
    // Source is the instrument-resolution cache key, so it stays pinned to what
    // manual Fidelity uploads already use even though this recipe reads JSON.
    expect(sourceFor(getRecipe("fidelity-uk")!, "Fidelity")).toBe("Fidelity:web:fidelity-csv");
  });
});

describe("renderExport", () => {
  const recipe = getRecipe("fidelity-uk")!;
  const window = { from: new Date(2026, 6, 2), to: new Date(2026, 6, 27) };

  it("substitutes the window in the site's own date format", () => {
    const req = renderExport(recipe, window);
    expect(req.url).toContain("fromDate=02/07/2026");
    expect(req.url).toContain("toDate=27/07/2026");
    expect(req.method).toBe("GET");
  });

  it("leaves the date separators unencoded", () => {
    // The site's own request sends literal slashes; percent-encoding them changes
    // the request the server sees.
    expect(renderExport(recipe, window).url).not.toContain("%2F");
  });

  it("does not mutate the recipe", () => {
    renderExport(recipe, window);
    expect(recipe.export.url).toContain("{{from}}");
  });

  it("carries the header that stands in for the site's own XHR", () => {
    expect(renderExport(recipe, window).headers).toMatchObject({
      "x-requested-with": "XMLHttpRequest",
    });
  });
});
