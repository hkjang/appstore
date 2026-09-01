import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { Button, Dialog, Input } from "./ui";

function DialogHarness() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>열기</Button>
      <Dialog open={open} title="키 생성" onClose={() => setOpen(false)}>
        <Input aria-label="키 이름" />
        <Button>저장</Button>
      </Dialog>
    </>
  );
}

describe("Dialog accessibility", () => {
  it("labels the dialog, closes with Escape and restores focus", async () => {
    const user = userEvent.setup();
    render(<DialogHarness />);
    const trigger = screen.getByRole("button", { name: "열기" });
    await user.click(trigger);
    expect(screen.getByRole("dialog", { name: "키 생성" })).toBeVisible();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
});
