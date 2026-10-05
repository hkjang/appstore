import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { FavoritesProvider } from "../features/apps/favorites";
import { AppFormPage } from "./app-form-page";
import { MyAppsPage } from "./personal-pages";

const missingId = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";

function myApp(overrides: Record<string, unknown> = {}) {
  return {
    id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    slug: "agent-hub",
    name: "Agent Hub",
    summary: "AI 에이전트 카탈로그",
    status: "draft",
    visibility: "public",
    ...overrides,
  };
}

/** Renders the production edit route, stubbing only the HTTP boundary. */
function renderEdit(
  myAppsResponse: () => Response,
  id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
) {
  const ok = (payload: unknown) =>
    new Response(JSON.stringify(payload), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/me/apps")) return Promise.resolve(myAppsResponse());
      if (url.includes("/auth/session"))
        return Promise.resolve(
          ok({ authenticated: true, user: { roles: ["user"] } }),
        );
      if (url.includes("/categories")) return Promise.resolve(ok([]));
      return Promise.resolve(ok({ items: [], total: 0 }));
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/my/apps/${id}/edit`]}>
        <AuthProvider>
          <FavoritesProvider>
            <Routes>
              <Route path="/my/apps" element={<MyAppsPage />} />
              <Route path="/my/apps/:id/edit" element={<AppFormPage edit />} />
            </Routes>
          </FavoritesProvider>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/** 200 OK whose list simply does not contain the requested app. */
const withoutTheApp = () =>
  new Response(JSON.stringify({ items: [myApp()], total: 1 }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });

describe("Edit screen for an app the owner cannot see", () => {
  it("names the missing app in Korean instead of leaking the query library's message", async () => {
    renderEdit(withoutTheApp, missingId);

    expect(
      await screen.findByRole("heading", { name: "앱을 찾을 수 없습니다" }),
    ).toBeVisible();
    // The owner must never read the query library's internal wording.
    const shown = document.body.textContent ?? "";
    for (const internal of [
      "Query data cannot be undefined",
      "queryFn",
      "undefined",
    ])
      expect(shown).not.toContain(internal);
    // A "없음" is not a transport failure, so the error panel stays away.
    expect(screen.queryByText("화면을 불러오지 못했습니다")).toBeNull();
    expect(screen.queryByRole("button", { name: "다시 시도" })).toBeNull();
  });

  it("offers a way back to the owner's app list", async () => {
    renderEdit(withoutTheApp, missingId);

    const heading = await screen.findByRole("heading", {
      name: "앱을 찾을 수 없습니다",
    });
    const back = screen.getByRole("link", { name: "내 앱 목록" });
    expect(heading.closest(".state-panel")).toContainElement(back);

    await userEvent.click(back);
    expect(
      await screen.findByRole("heading", { name: "내가 등록한 앱" }),
    ).toBeVisible();
  });
});

describe("Edit screen for an app the owner owns", () => {
  it("fills the form with the stored values", async () => {
    renderEdit(
      () =>
        new Response(
          JSON.stringify({
            items: [myApp({ name: "Agent Hub", slug: "agent-hub" })],
            total: 1,
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
    );

    expect(await screen.findByLabelText("앱 이름")).toHaveValue("Agent Hub");
    expect(screen.getByLabelText("Slug")).toHaveValue("agent-hub");
    expect(
      screen.queryByRole("heading", { name: "앱을 찾을 수 없습니다" }),
    ).toBeNull();
  });

  it("still reports a real transport failure as an error, not as a missing app", async () => {
    renderEdit(
      () =>
        new Response(
          JSON.stringify({
            code: "internal",
            message: "서버 오류가 발생했습니다.",
          }),
          { status: 500, headers: { "content-type": "application/json" } },
        ),
    );

    expect(
      await screen.findByRole("heading", {
        name: "화면을 불러오지 못했습니다",
      }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "다시 시도" })).toBeVisible();
    expect(
      screen.queryByRole("heading", { name: "앱을 찾을 수 없습니다" }),
    ).toBeNull();
  });
});
