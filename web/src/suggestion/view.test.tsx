// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { useDiffView } from "./view";

afterEach(cleanup);
beforeEach(() => localStorage.clear());

describe("useDiffView", () => {
  it("defaults to split with no stored preference", () => {
    const { result } = renderHook(() => useDiffView());
    expect(result.current[0]).toBe("split");
  });

  it("persists the third 'rendered' mode and reads it back on remount", () => {
    const { result, unmount } = renderHook(() => useDiffView());
    act(() => result.current[1]("rendered"));
    expect(result.current[0]).toBe("rendered");
    expect(localStorage.getItem("outbox-diff-view")).toBe("rendered");

    unmount();
    const { result: r2 } = renderHook(() => useDiffView());
    expect(r2.current[0]).toBe("rendered"); // survived across sessions
  });

  it("still honours the existing split/inline values", () => {
    localStorage.setItem("outbox-diff-view", "inline");
    expect(renderHook(() => useDiffView()).result.current[0]).toBe("inline");
    localStorage.setItem("outbox-diff-view", "split");
    expect(renderHook(() => useDiffView()).result.current[0]).toBe("split");
  });

  it("falls back to split for an unknown stored value", () => {
    localStorage.setItem("outbox-diff-view", "bogus");
    expect(renderHook(() => useDiffView()).result.current[0]).toBe("split");
  });
});
