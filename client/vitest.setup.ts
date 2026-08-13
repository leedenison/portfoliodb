import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Vitest globals are off, so testing-library's own auto-cleanup never
// registers. Without this a component rendered by one test is still mounted
// during the next, and a query that should find one element finds two.
afterEach(cleanup);
