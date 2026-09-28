import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { FavoritesProvider } from "../features/apps/favorites";
import type { StoreApp } from "../types";
import { AppDetailPage, AppsPage } from "./public-pages";

describe("Apps route state", () => {
  it("shows a catalog favorite on /favorites and removes it when toggled off", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((input: RequestInfo | URL) => {
        const url = String(input);
        const payload = url.includes("/categories")
          ? []
          : url.includes("/auth/session")
            ? { authenticated: false }
            : {
                items: [
                  {
                    id: "1",
                    slug: "agent-hub",
                    name: "Agent Hub",
                    summary: "AI",
                    status: "published",
                    visibility: "public",
                  },
                ],
                total: 1,
              };
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
        <MemoryRouter initialEntries={["/apps"]}>
          <AuthProvider>
            <FavoritesProvider>
              <Link to="/favorites">즐겨찾기로 이동</Link>
              <Routes>
                <Route path="/apps" element={<AppsPage />} />
                <Route path="/favorites" element={<AppsPage favoritesOnly />} />
              </Routes>
            </FavoritesProvider>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const add = await screen.findByRole("button", {
      name: "Agent Hub 즐겨찾기 추가",
    });
    expect(add).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(add);
    expect(
      screen.getByRole("button", { name: "Agent Hub 즐겨찾기 해제" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(JSON.parse(localStorage.getItem("appstore.favorites")!)).toEqual([
      "agent-hub",
    ]);
    await userEvent.click(
      screen.getByRole("link", { name: "즐겨찾기로 이동" }),
    );
    expect(
      await screen.findByRole("heading", { name: "즐겨찾기" }),
    ).toBeVisible();
    expect(
      await screen.findByRole("heading", { name: "Agent Hub" }),
    ).toBeVisible();
    await userEvent.click(
      screen.getByRole("button", { name: "Agent Hub 즐겨찾기 해제" }),
    );
    expect(screen.queryByRole("heading", { name: "Agent Hub" })).toBeNull();
    expect(JSON.parse(localStorage.getItem("appstore.favorites")!)).toEqual([]);
  });

  const jsonResponse = (payload: unknown) =>
    new Response(JSON.stringify(payload), {
      status: 200,
      headers: { "content-type": "application/json" },
    });

  // The catalog answer is held back until the test releases it, which is how a
  // real network makes a request in flight visible: while one is open the page
  // has no list to draw and falls back to the loading placeholders.
  const heldCatalog = (apps: [slug: string, name: string][]) => {
    const waiting: (() => void)[] = [];
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/categories")) return Promise.resolve(jsonResponse([]));
      if (url.includes("/auth/session"))
        return Promise.resolve(jsonResponse({ authenticated: false }));
      return new Promise<Response>((resolve) =>
        waiting.push(() =>
          resolve(
            jsonResponse({
              items: apps.map(([slug, name], index) => ({
                id: String(index + 1),
                slug,
                name,
                summary: "AI",
                status: "published",
                visibility: "public",
              })),
              total: apps.length,
              limit: 24,
              offset: 0,
            }),
          ),
        ),
      );
    });
    return {
      fetchMock,
      requests: () =>
        fetchMock.mock.calls.filter(([url]) =>
          String(url).includes("/v1/apps?"),
        ).length,
      answer: () => waiting.splice(0).forEach((send) => send()),
    };
  };

  const renderCatalog = (fetchMock: unknown, favoritesOnly = false) => {
    vi.stubGlobal("fetch", fetchMock);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[favoritesOnly ? "/favorites" : "/apps"]}>
          <AuthProvider>
            <FavoritesProvider>
              <AppsPage favoritesOnly={favoritesOnly} />
            </FavoritesProvider>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  };

  it("keeps the catalog grid on screen and skips a refetch when a heart is toggled", async () => {
    const catalog = heldCatalog([["agent-hub", "Agent Hub"]]);
    renderCatalog(catalog.fetchMock);
    await waitFor(() => expect(catalog.requests()).toBe(1));
    catalog.answer();
    const add = await screen.findByRole("button", {
      name: "Agent Hub 즐겨찾기 추가",
    });

    await userEvent.click(add);

    // The heart is browser-local state the list does not depend on, so the
    // grid stays as it was instead of being asked for again.
    expect(screen.getByRole("heading", { name: "Agent Hub" })).toBeVisible();
    expect(screen.queryByLabelText("앱 목록을 불러오는 중")).toBeNull();
    expect(screen.getByText("1개 앱")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Agent Hub 즐겨찾기 해제" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(JSON.parse(localStorage.getItem("appstore.favorites")!)).toEqual([
      "agent-hub",
    ]);
    expect(catalog.requests()).toBe(1);
  });

  it("removes an unfavorited card on /favorites without reloading the list", async () => {
    localStorage.setItem(
      "appstore.favorites",
      JSON.stringify(["agent-hub", "docs-hub"]),
    );
    const catalog = heldCatalog([
      ["agent-hub", "Agent Hub"],
      ["docs-hub", "Docs Hub"],
    ]);
    renderCatalog(catalog.fetchMock, true);
    await waitFor(() => expect(catalog.requests()).toBe(1));
    catalog.answer();
    const remove = await screen.findByRole("button", {
      name: "Agent Hub 즐겨찾기 해제",
    });

    await userEvent.click(remove);

    expect(screen.queryByRole("heading", { name: "Agent Hub" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Docs Hub" })).toBeVisible();
    expect(screen.queryByLabelText("앱 목록을 불러오는 중")).toBeNull();
    expect(JSON.parse(localStorage.getItem("appstore.favorites")!)).toEqual([
      "docs-hub",
    ]);
    expect(catalog.requests()).toBe(1);
  });

  const renderSecondPage = (view = "grid") => {
    const items: StoreApp[] = Array.from({ length: 25 }, (_, index) => ({
      id: String(index + 1),
      slug: `app-${index + 1}`,
      name: `Catalog App ${index + 1}`,
      summary: "Catalog entry",
      status: "published",
      visibility: "public",
    }));
    const requests: URL[] = [];
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/apps") {
        requests.push(url);
        const offset = Number(url.searchParams.get("offset"));
        const limit = Number(url.searchParams.get("limit"));
        return Promise.resolve(
          jsonResponse({
            items: items.slice(offset, offset + limit),
            total: items.length,
            limit,
            offset,
          }),
        );
      }
      return Promise.resolve(
        jsonResponse(
          url.pathname === "/api/v1/categories"
            ? [{ id: "ai", slug: "ai", name: "AI" }]
            : { authenticated: false },
        ),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter
          initialEntries={[
            `/apps?page=2&q=Catalog&category=ai&sort=name&view=${view}`,
          ]}
        >
          <AuthProvider>
            <FavoritesProvider>
              <AppsPage />
            </FavoritesProvider>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    return requests;
  };

  it.each([
    ["grid", "목록"],
    ["list", "카드"],
  ])("keeps page two when changing the %s view", async (view, button) => {
    const requests = renderSecondPage(view);
    await screen.findByRole("heading", { name: "Catalog App 25" });
    await userEvent.click(screen.getByRole("button", { name: button }));

    expect(screen.getByText("2 페이지")).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "Catalog App 25" }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: button })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByLabelText("앱 검색")).toHaveValue("Catalog");
    expect(screen.getByLabelText("카테고리")).toHaveValue("ai");
    expect(screen.getByLabelText("정렬")).toHaveValue("name");
    expect(requests).toHaveLength(1);
    expect(requests[0]?.searchParams.get("offset")).toBe("24");
  });

  it.each(["search", "category", "sort"])(
    "still resets to page one after a %s change",
    async (control) => {
      const requests = renderSecondPage("list");
      await screen.findByRole("heading", { name: "Catalog App 25" });
      if (control === "search") {
        await userEvent.type(screen.getByLabelText("앱 검색"), " App");
        await userEvent.click(screen.getByRole("button", { name: "검색" }));
      } else if (control === "category") {
        await userEvent.selectOptions(screen.getByLabelText("카테고리"), "");
      } else {
        await userEvent.selectOptions(screen.getByLabelText("정렬"), "updated");
      }
      expect(
        await screen.findByRole("heading", { name: "Catalog App 1" }),
      ).toBeVisible();
      expect(screen.getByText("1 페이지")).toBeVisible();
      expect(screen.getByRole("button", { name: "목록" })).toHaveAttribute(
        "aria-pressed",
        "true",
      );
      expect(requests.at(-1)?.searchParams.get("offset")).toBe("0");
    },
  );

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
