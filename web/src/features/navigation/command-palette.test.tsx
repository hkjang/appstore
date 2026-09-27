import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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
