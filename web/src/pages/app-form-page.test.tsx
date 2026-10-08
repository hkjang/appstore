import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { FavoritesProvider } from "../features/apps/favorites";
import { AppFormPage } from "./app-form-page";
import { MyAppsPage } from "./personal-pages";

const category = {
  id: "11111111-1111-4111-8111-111111111111",
  slug: "ai",
  name: "AI",
};

const missingId = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";

function myApp(overrides: Record<string, unknown> = {}) {
  return {
    id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    slug: "agent-hub",
    name: "Agent Hub",
    summary: "AI 에이전트 카탈로그",
    description: "팀의 에이전트를 탐색합니다.",
    serviceUrl: "https://agent.example.internal",
    category,
    status: "draft",
    visibility: "public",
    ...overrides,
  };
}

function jsonResponse(payload: unknown, status = 200) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "content-type": "application/json" },
  });
}

/** Production pages, router and providers; only HTTP responses are replaced. */
function renderForm({
  path = "/submit",
  myAppsResponse = () =>
    jsonResponse({ items: [myApp({ status: "published" })], total: 1 }),
  saveResponse = () => jsonResponse(myApp(), 201),
  uploadResponse = () => Promise.resolve(jsonResponse({})),
}: {
  path?: string;
  myAppsResponse?: () => Response;
  saveResponse?: () => Response;
  uploadResponse?: () => Promise<Response>;
} = {}) {
  const fetch = vi
    .fn()
    .mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (method === "GET" && url === "/api/v1/me/apps")
        return Promise.resolve(myAppsResponse());
      if (method === "POST" && url === "/api/v1/apps")
        return Promise.resolve(saveResponse());
      if (method === "PUT" && url === `/api/v1/apps/${myApp().id}`)
        return Promise.resolve(saveResponse());
      if (method === "POST" && url === `/api/v1/apps/${myApp().id}/documents`)
        return uploadResponse();
      if (method === "GET" && url === "/api/v1/auth/session")
        return Promise.resolve(
          jsonResponse({ authenticated: true, user: { roles: ["user"] } }),
        );
      if (method === "GET" && url === "/api/v1/categories")
        return Promise.resolve(jsonResponse([category]));
      if (method === "GET" && url === `/api/v1/apps/${myApp().id}/documents`)
        return Promise.resolve(jsonResponse({ items: [], total: 0 }));
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <FavoritesProvider>
            <Routes>
              <Route path="/submit" element={<AppFormPage />} />
              <Route path="/my/apps" element={<MyAppsPage />} />
              <Route path="/my/apps/:id/edit" element={<AppFormPage edit />} />
            </Routes>
          </FavoritesProvider>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetch;
}

function renderEdit(myAppsResponse: () => Response, id = myApp().id) {
  return renderForm({ path: `/my/apps/${id}/edit`, myAppsResponse });
}

async function fillRegistration() {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("앱 이름"), "Agent Hub");
  await user.type(screen.getByLabelText("Slug"), "agent-hub");
  await user.type(screen.getByLabelText("한 줄 설명"), "AI 에이전트 카탈로그");
  await user.type(
    screen.getByLabelText("서비스 URL"),
    "https://agent.example.internal",
  );
  await user.selectOptions(screen.getByLabelText("카테고리"), category.id);
  await user.type(
    screen.getByLabelText("상세 설명"),
    "팀의 에이전트를 탐색합니다.",
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

describe("Saved application completion", () => {
  it.each([
    ["draft", "초안"],
    ["published", "게시됨"],
    ["pending_review", "검토 대기"],
    [undefined, "—"],
    ["quarantined", "quarantined"],
  ])("shows the POST response status %s as %s", async (status, label) => {
    renderForm({ saveResponse: () => jsonResponse(myApp({ status }), 201) });
    await fillRegistration();
    await userEvent.click(screen.getByRole("button", { name: "등록" }));

    const heading = await screen.findByRole("heading", {
      name: "앱이 등록되었습니다",
    });
    expect(heading.closest(".state-panel")).toHaveTextContent(
      `현재 상태: ${label}`,
    );
    expect(heading.closest(".state-panel")).not.toHaveTextContent(
      /즉시 게시|승인 Workflow|보안 심의/,
    );
  });

  it.each([
    ["pending_review", "검토 대기"],
    ["rejected", "반려"],
    ["archived", "보관됨"],
  ])(
    "shows PUT status %s instead of the previously published status",
    async (status, label) => {
      renderForm({
        path: `/my/apps/${myApp().id}/edit`,
        saveResponse: () => jsonResponse(myApp({ status })),
      });
      expect(await screen.findByLabelText("앱 이름")).toHaveValue("Agent Hub");
      await userEvent.click(screen.getByRole("button", { name: "변경 저장" }));

      const heading = await screen.findByRole("heading", {
        name: "앱이 수정되었습니다",
      });
      expect(heading.closest(".state-panel")).toHaveTextContent(
        `현재 상태: ${label}`,
      );
      expect(heading.closest(".state-panel")).not.toHaveTextContent(
        /게시됨|즉시 게시/,
      );
    },
  );

  it.each([false, true])(
    "navigates to the owner's apps after saving (edit=%s)",
    async (edit) => {
      renderForm({ path: edit ? `/my/apps/${myApp().id}/edit` : "/submit" });
      if (!edit) await fillRegistration();
      await userEvent.click(
        await screen.findByRole("button", {
          name: edit ? "변경 저장" : "등록",
        }),
      );
      await userEvent.click(
        await screen.findByRole("button", { name: "내 앱으로 이동" }),
      );
      expect(
        await screen.findByRole("heading", { name: "내가 등록한 앱" }),
      ).toBeVisible();
    },
  );

  it.each([false, true])(
    "keeps the form error after a save failure (edit=%s)",
    async (edit) => {
      renderForm({
        path: edit ? `/my/apps/${myApp().id}/edit` : "/submit",
        saveResponse: () =>
          jsonResponse(
            { code: "internal", message: "앱 저장에 실패했습니다." },
            500,
          ),
      });
      if (!edit) await fillRegistration();
      await userEvent.click(
        await screen.findByRole("button", {
          name: edit ? "변경 저장" : "등록",
        }),
      );
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "앱 저장에 실패했습니다.",
      );
      expect(screen.getByLabelText("앱 이름")).toHaveValue("Agent Hub");
      expect(
        screen.queryByRole("heading", { name: /앱이 (등록|수정)되었습니다/ }),
      ).toBeNull();
    },
  );

  it.each([false, true])(
    "keeps the form error when documents fail after saving (edit=%s)",
    async (edit) => {
      const fetch = renderForm({
        path: edit ? `/my/apps/${myApp().id}/edit` : "/submit",
        uploadResponse: async () =>
          jsonResponse(
            { code: "internal", message: "문서 저장에 실패했습니다." },
            500,
          ),
      });
      if (!edit) await fillRegistration();
      await userEvent.upload(
        await screen.findByLabelText("가이드 문서 파일 선택"),
        new File(["guide"], "guide.txt", { type: "text/plain" }),
      );
      await userEvent.click(
        screen.getByRole("button", {
          name: edit ? "변경 저장" : "등록",
        }),
      );
      await waitFor(() =>
        expect(
          screen.getByRole("button", {
            name: edit ? "변경 저장" : "등록",
          }),
        ).toBeEnabled(),
      );
      expect(
        screen
          .getAllByRole("alert")
          .find((alert) => alert.classList.contains("notice")),
      ).toHaveTextContent("문서 저장에 실패했습니다.");
      expect(fetch).toHaveBeenCalledWith(
        `/api/v1/apps/${myApp().id}/documents`,
        expect.objectContaining({ method: "POST", body: expect.any(FormData) }),
      );
      expect(screen.getByLabelText("앱 이름")).toHaveValue("Agent Hub");
      expect(
        screen.queryByRole("heading", { name: /앱이 (등록|수정)되었습니다/ }),
      ).toBeNull();
    },
  );

  it("waits for document application before showing the saved status", async () => {
    let finishUpload!: (response: Response) => void;
    const upload = new Promise<Response>((resolve) => {
      finishUpload = resolve;
    });
    const fetch = renderForm({ uploadResponse: () => upload });
    await fillRegistration();
    await userEvent.upload(
      screen.getByLabelText("가이드 문서 파일 선택"),
      new File(["guide"], "guide.txt", { type: "text/plain" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "등록" }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        `/api/v1/apps/${myApp().id}/documents`,
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(
      screen.queryByRole("heading", { name: "앱이 등록되었습니다" }),
    ).toBeNull();
    expect(screen.getByRole("button", { name: "저장 중…" })).toBeDisabled();
    finishUpload(jsonResponse({ id: "document-id" }, 201));
    const heading = await screen.findByRole("heading", {
      name: "앱이 등록되었습니다",
    });
    expect(heading.closest(".state-panel")).toHaveTextContent(
      "현재 상태: 초안",
    );
  });
});
