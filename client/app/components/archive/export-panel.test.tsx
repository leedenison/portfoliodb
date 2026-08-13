import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import type { ArchivePartOption } from "@/lib/archive/parts";
import { ExportArchivePanel } from "./export-panel";

const OPTIONS: ArchivePartOption[] = [
  {
    part: ArchivePart.PREFERENCES,
    label: "Preferences",
    note: "Display currency, and the asset classes ignored on import.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.TXS,
    label: "Transactions",
    note: "Every posting.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.DECLARATIONS,
    label: "Holding declarations",
    note: "What you stated you held, and when.",
    defaultSelected: false,
  },
];

function renderPanel(build = vi.fn().mockResolvedValue("{}")) {
  render(<ExportArchivePanel options={OPTIONS} build={build} filename="archive.json" />);
  return build;
}

describe("ExportArchivePanel", () => {
  it("starts with the parts the menu says are selected by default", () => {
    renderPanel();
    expect(screen.getByLabelText(/Preferences/)).toBeChecked();
    expect(screen.getByLabelText(/Transactions/)).toBeChecked();
    // Plugin config is why this is a per-part decision rather than a blanket
    // one: a part can be offered without being asked for by default.
    expect(screen.getByLabelText(/Holding declarations/)).not.toBeChecked();
  });

  it("asks for the ticked parts in menu order, not tick order", async () => {
    const build = renderPanel();

    // Ticked last, and still exported in the order the menu states it, which is
    // restore order.
    fireEvent.click(screen.getByLabelText(/Holding declarations/));
    fireEvent.click(screen.getByLabelText(/Preferences/));
    fireEvent.click(screen.getByLabelText(/Preferences/));
    fireEvent.click(screen.getByTestId("export-archive"));

    await waitFor(() => expect(build).toHaveBeenCalledTimes(1));
    expect(build).toHaveBeenCalledWith([
      ArchivePart.PREFERENCES,
      ArchivePart.TXS,
      ArchivePart.DECLARATIONS,
    ]);
  });

  it("leaves an unticked part out of the request", async () => {
    const build = renderPanel();

    fireEvent.click(screen.getByLabelText(/Transactions/));
    fireEvent.click(screen.getByTestId("export-archive"));

    // Absent from the request rather than requested and dropped: what the file
    // does not carry is what was not asked for.
    await waitFor(() => expect(build).toHaveBeenCalledWith([ArchivePart.PREFERENCES]));
  });

  it("cannot export nothing", () => {
    const build = renderPanel();

    fireEvent.click(screen.getByLabelText(/Preferences/));
    fireEvent.click(screen.getByLabelText(/Transactions/));

    expect(screen.getByTestId("export-archive")).toBeDisabled();
    fireEvent.click(screen.getByTestId("export-archive"));
    expect(build).not.toHaveBeenCalled();
  });

  it("says so when the export fails, and stays usable", async () => {
    const build = renderPanel(vi.fn().mockRejectedValue(new Error("stream broke")));

    fireEvent.click(screen.getByTestId("export-archive"));

    expect(await screen.findByText("stream broke")).toBeInTheDocument();
    // The button comes back rather than being left saying "Exporting...", so a
    // failed export can be retried without reloading the page.
    await waitFor(() => expect(screen.getByTestId("export-archive")).toBeEnabled());
    expect(build).toHaveBeenCalledTimes(1);
  });
});
