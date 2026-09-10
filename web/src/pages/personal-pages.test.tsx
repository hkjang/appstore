import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { FavoritesProvider } from "../features/apps/favorites";
import { MyAppsPage } from "./personal-pages";

function myApp(overrides: Record<string, unknown>) {
  return {
    id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    slug: "agent-hub",
    name: "Agent Hub",
    summary: "AI 에이전트 카탈로그",
    ...overrides,
  };
}

function renderMyApps(items: Record<string, unknown>[]) {
  const fetchMock = vi.fn().mockImplementation(() =>
    Promise.resolve(
      new Response(JSON.stringify({ items, total: items.length }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/my/apps"]}>
        <AuthProvider>
          <FavoritesProvider>
            <MyAppsPage />
          </FavoritesProvider>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("My applications", () => {
  it("shows why a rejected app was sent back", async () => {
    renderMyApps([
      myApp({
        status: "rejected",
        review: {
          id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
          appId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
          status: "rejected",
          level: 1,
          reason: "서비스 URL이 사내망에서 열리지 않습니다.",
          reviewerName: "검토자 김",
          decidedAt: "2026-09-01T08:00:00Z",
        },
      }),
    ]);

    const notice = await screen.findByRole("alert");
    expect(notice).toHaveTextContent(
      "서비스 URL이 사내망에서 열리지 않습니다.",
    );
    expect(notice).toHaveTextContent("검토자 김");
    expect(notice).toHaveTextContent("1단계 검토");
  });

  it("keeps a superseded reason hidden once the app is back in review", async () => {
    renderMyApps([
      myApp({
        status: "pending_review",
        review: {
          id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
          appId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
          status: "pending",
          level: 1,
        },
      }),
    ]);

    expect(
      await screen.findByRole("heading", { name: "Agent Hub" }),
    ).toBeVisible();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
