import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../../app/providers";
import { CommandPalette } from "./command-palette";

const RECENT_KEY = "appstore.recentDestinations";

// What an admin's browser leaves behind after visiting the admin console.
const STORED_RECENT = [
  { id: "menu:/admin/apps", to: "/admin/apps", label: "앱 관리" },
  {
    id: "app-admin:42",
    to: "/admin/apps/42",
    label: "데모 앱 · 관리 설정",
  },
  { id: "menu:/apps", to: "/apps", label: "전체 앱" },
  { id: "app:demo", to: "/apps/demo", label: "데모 앱" },
];

function json(payload: unknown) {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}

async function renderPalette(session: Record<string, unknown>) {
  const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
    const target = String(input);
    if (target.includes("/auth/session")) return Promise.resolve(json(session));
    if (target.includes("/public/config")) {
      return Promise.resolve(json({ siteName: "AppStore" }));
    }
    return Promise.reject(new Error(`unexpected request: ${target}`));
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/"]}>
        <AuthProvider>
          <CommandPalette open onClose={vi.fn()} />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  // The recent list is filtered against the live session, so nothing may be
  // asserted while the session request is still in flight.
  await waitFor(() => expect(client.isFetching()).toBe(0));
  return fetchMock;
}

describe("CommandPalette 최근 이동", () => {
  it("hides recent destinations the anonymous session may not reach", async () => {
    localStorage.setItem(RECENT_KEY, JSON.stringify(STORED_RECENT));

    await renderPalette({ authenticated: false });

    expect(screen.queryByText("앱 관리")).not.toBeInTheDocument();
    expect(screen.queryByText("데모 앱 · 관리 설정")).not.toBeInTheDocument();
    // The store menu is open to everyone, so these two stay.
    expect(screen.getByText("최근 이동")).toBeInTheDocument();
    expect(screen.getByText("전체 앱")).toBeInTheDocument();
    expect(screen.getByText("데모 앱")).toBeInTheDocument();
    // Filtering is for display only: a later sign-in restores the list.
    expect(localStorage.getItem(RECENT_KEY)).toBe(
      JSON.stringify(STORED_RECENT),
    );
  });

  it("keeps the same recent destinations for an admin session", async () => {
    localStorage.setItem(RECENT_KEY, JSON.stringify(STORED_RECENT));

    await renderPalette({
      authenticated: true,
      user: { roles: ["admin"] },
    });

    expect(screen.getByText("앱 관리")).toBeInTheDocument();
    expect(screen.getByText("데모 앱 · 관리 설정")).toBeInTheDocument();
    expect(screen.getByText("전체 앱")).toBeInTheDocument();
    expect(screen.getByText("데모 앱")).toBeInTheDocument();
  });
});

describe("CommandPalette 검색 선택", () => {
  // The catalogue answer is held back until the test releases it, which is how
  // a slow connection makes app results land after the user has already
  // picked a menu entry with the arrow keys.
  function heldApps(apps: [slug: string, name: string][]) {
    const waiting: (() => void)[] = [];
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const target = String(input);
      if (target.includes("/auth/session")) {
        return Promise.resolve(json({ authenticated: false }));
      }
      if (target.includes("/public/config")) {
        return Promise.resolve(json({ siteName: "AppStore" }));
      }
      if (target.includes("/v1/apps?")) {
        return new Promise<Response>((resolve) =>
          waiting.push(() =>
            resolve(
              json({
                items: apps.map(([slug, name], index) => ({
                  id: String(index + 1),
                  slug,
                  name,
                  summary: "AI",
                  status: "published",
                  visibility: "public",
                })),
                total: apps.length,
                limit: 6,
                offset: 0,
              }),
            ),
          ),
        );
      }
      return Promise.reject(new Error(`unexpected request: ${target}`));
    });
    return {
      fetchMock,
      requests: () =>
        fetchMock.mock.calls.filter(([url]) =>
          String(url).includes("/v1/apps?"),
        ).length,
      answer: () => waiting.splice(0).forEach((send) => send()),
    };
  }

  const selected = () => screen.getByRole("option", { selected: true });

  let client: QueryClient;

  // Deliberately not the renderPalette helper above: that one waits for every
  // request to settle, which a held app answer never does.
  function renderSearchPalette(fetchMock: unknown) {
    vi.stubGlobal("fetch", fetchMock);
    client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={["/"]}>
          <AuthProvider>
            <CommandPalette open onClose={vi.fn()} />
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  }

  it("keeps the picked menu entry when app results arrive late", async () => {
    const catalog = heldApps([["agent-hub", "Agent Hub"]]);
    renderSearchPalette(catalog.fetchMock);
    // Let the session settle first: the menu list depends on it, and the new
    // assertions need the app request to be the only one still open.
    await waitFor(() => expect(client.isFetching()).toBe(0));

    await userEvent.type(screen.getByLabelText("빠른 이동 검색"), "앱");
    await waitFor(() => expect(catalog.requests()).toBe(1));
    await waitFor(() => expect(selected()).toHaveTextContent("전체 앱"));

    await userEvent.keyboard("{ArrowDown}");
    expect(selected()).toHaveTextContent("MCP 앱");

    catalog.answer();
    await screen.findByText("Agent Hub");
    // The app result is inserted ahead of the menu matches, so tracking the
    // selection by array index would slide the highlight onto "전체 앱".
    expect(selected()).toHaveTextContent("MCP 앱");

    await userEvent.keyboard("{Enter}");
    expect(JSON.parse(localStorage.getItem(RECENT_KEY)!)[0].id).toBe(
      "menu:/apps?mcp=true",
    );
  });

  it("wraps the arrow selection at both ends of the list", async () => {
    const catalog = heldApps([["agent-hub", "Agent Hub"]]);
    renderSearchPalette(catalog.fetchMock);
    await waitFor(() => expect(client.isFetching()).toBe(0));

    // The palette focuses its input on a frame callback, so take the focus
    // explicitly rather than racing it: arrow keys are read on the dialog.
    await userEvent.click(screen.getByLabelText("빠른 이동 검색"));
    const labels = screen.getAllByRole("option").map((row) => row.textContent);
    expect(labels.length).toBeGreaterThan(2);
    expect(selected().textContent).toBe(labels[0]);

    await userEvent.keyboard("{ArrowUp}");
    expect(selected().textContent).toBe(labels[labels.length - 1]);
    await userEvent.keyboard("{ArrowDown}");
    expect(selected().textContent).toBe(labels[0]);
    await userEvent.keyboard("{ArrowDown}");
    expect(selected().textContent).toBe(labels[1]);
  });

  it("resets the selection to the first row when the query changes", async () => {
    const catalog = heldApps([["agent-hub", "Agent Hub"]]);
    renderSearchPalette(catalog.fetchMock);
    await waitFor(() => expect(client.isFetching()).toBe(0));

    const input = screen.getByLabelText("빠른 이동 검색");
    await userEvent.click(input);
    // Land on "MCP 앱", which also survives the "앱" filter below — so only a
    // real reset, not the list shrinking, can move the highlight back.
    await userEvent.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}");
    expect(selected()).toHaveTextContent("MCP 앱");

    await userEvent.type(input, "앱");
    await waitFor(() => expect(catalog.requests()).toBe(1));
    expect(screen.getByText("MCP 앱")).toBeInTheDocument();
    expect(selected()).toHaveTextContent("전체 앱");
  });
});
