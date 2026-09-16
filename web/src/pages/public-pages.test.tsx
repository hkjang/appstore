import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { FavoritesProvider } from "../features/apps/favorites";
import { AppDetailPage, AppsPage } from "./public-pages";

describe("Apps route state", () => {
  it("restores search, category and sort controls from the URL", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      const payload = url.includes("/categories")
        ? [{ id: "ai", slug: "ai", name: "AI" }]
        : {
            items: [
              {
                id: "1",
                slug: "agent-hub",
                name: "Agent Hub",
                summary: "AI 에이전트 카탈로그",
                category: { id: "ai", slug: "ai", name: "AI" },
                status: "published",
              },
            ],
            total: 1,
            limit: 24,
            offset: 0,
          };
      return Promise.resolve(
        new Response(JSON.stringify(payload), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={client}>
        <MemoryRouter
          initialEntries={["/apps?q=agent&category=ai&sort=trending"]}
        >
          <AuthProvider>
            <FavoritesProvider>
              <AppsPage />
            </FavoritesProvider>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByLabelText("앱 검색")).toHaveValue("agent");
    expect(screen.getByLabelText("정렬")).toHaveValue("trending");
    await waitFor(() =>
      expect(screen.getByLabelText("카테고리")).toHaveValue("ai"),
    );
    expect(
      await screen.findByRole("heading", { name: "Agent Hub" }),
    ).toBeVisible();
    // Everything in the public catalog is published; only an owner's own
    // list labels cards with their status.
    expect(screen.queryByText("게시됨")).toBeNull();
    expect(
      fetchMock.mock.calls.some(([url]) => String(url).includes("q=agent")),
    ).toBe(true);
  });
});

describe("App detail introduction", () => {
  // jsdom reports 0 for both heights, which is also what it reports after
  // these stubs are put back, so the reset is the plain jsdom answer.
  const stubHeight = (name: "scrollHeight" | "clientHeight", value: number) =>
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      get: () => value,
    });
  afterEach(() => {
    stubHeight("scrollHeight", 0);
    stubHeight("clientHeight", 0);
  });

  const renderDetail = (description: string) => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((input: RequestInfo | URL) => {
        const url = String(input);
        const payload = url.includes("/documents")
          ? { items: [] }
          : url.includes("/apps/agent-hub")
            ? {
                id: "1",
                slug: "agent-hub",
                name: "Agent Hub",
                summary: "AI 에이전트 카탈로그",
                description,
                status: "published",
              }
            : {};
        return Promise.resolve(
          new Response(JSON.stringify(payload), {
            status: 200,
            headers: { "content-type": "application/json" },
          }),
        );
      }),
    );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={["/apps/agent-hub"]}>
          <AuthProvider>
            <FavoritesProvider>
              <Routes>
                <Route path="/apps/:slug" element={<AppDetailPage />} />
              </Routes>
            </FavoritesProvider>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  };

  it("folds a long description away so what follows it stays on screen", async () => {
    // jsdom lays nothing out, so the overflow the clamp would produce in a
    // browser is stated here.
    stubHeight("scrollHeight", 600);
    stubHeight("clientHeight", 200);
    renderDetail("긴 소개 ".repeat(200));
    const intro = await screen.findByText(/긴 소개/);
    expect(intro).toHaveClass("is-clamped");

    await userEvent.click(screen.getByRole("button", { name: /더 보기/ }));
    expect(intro).not.toHaveClass("is-clamped");
    expect(screen.getByRole("button", { name: /접기/ })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("leaves a short description alone", async () => {
    stubHeight("scrollHeight", 120);
    stubHeight("clientHeight", 200);
    renderDetail("한 줄짜리 소개");
    expect(await screen.findByText("한 줄짜리 소개")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /더 보기/ })).toBeNull();
  });
});
