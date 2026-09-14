import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AdminMailPage } from "./admin-pages";

const settings = {
  enabled: true,
  smtpHost: "relay.corp.example",
  smtpPort: 25,
  security: "auto",
  skipTlsVerify: false,
  username: "notifier",
  passwordSet: true,
  fromAddress: "appstore@corp.example",
  fromName: "AppStore",
  baseUrl: "",
  timeoutSeconds: 10,
  events: { "review.requested": true, "review.decided": false },
  availableEvents: [
    {
      name: "review.requested",
      switch: "review_requested",
      label: "검토 요청",
      help: "",
    },
    {
      name: "review.decided",
      switch: "review_decided",
      label: "검토 결과",
      help: "",
    },
  ],
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function renderMail() {
  const calls: { url: string; method: string; body?: string }[] = [];
  const fetchMock = vi
    .fn()
    .mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ url, method, body: init?.body as string | undefined });
      if (url.includes("/admin/mail/deliveries")) {
        return Promise.resolve(
          json({
            items: [
              {
                id: "d-1",
                event: "review.requested",
                recipient: "reviewer@corp.example",
                subject: "[AppStore] 'Radar' 앱이 검토를 기다립니다",
                status: "failed",
                attempts: 2,
                errorMessage: "SMTP 연결 실패: connection refused",
                createdAt: "2026-09-14T09:00:00Z",
                updatedAt: "2026-09-14T09:00:04Z",
              },
            ],
            total: 1,
            status: { failed: 1 },
          }),
        );
      }
      if (url.endsWith("/admin/mail/test")) {
        return Promise.resolve(
          json({ sent: true, recipient: "me@corp.example" }),
        );
      }
      return Promise.resolve(json(settings));
    });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/admin/mail"]}>
        <AdminMailPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return calls;
}

describe("Mail settings", () => {
  it("shows only that a password is set and never sends one back unchanged", async () => {
    const calls = renderMail();
    const user = userEvent.setup();

    expect(
      await screen.findByText(/설정됨\. 기존 값은 조회되지 않습니다/),
    ).toBeVisible();
    expect(screen.getByLabelText("비밀번호")).toHaveValue("");
    // The switched-off event kind renders off; the other on.
    expect(
      screen.getByRole("checkbox", { name: "검토 결과" }),
    ).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "검토 요청" })).toBeChecked();

    await user.click(screen.getByRole("button", { name: /설정 저장/ }));
    await waitFor(() =>
      expect(
        calls.some(
          (call) => call.method === "PUT" && call.url.endsWith("/admin/mail"),
        ),
      ).toBe(true),
    );
    const saved = calls.find((call) => call.method === "PUT")!;
    expect(saved.body).not.toContain("password");
    expect(saved.body).toContain('"review.decided":false');
  });

  it("sends a test message and lists every delivery attempt with its outcome", async () => {
    const calls = renderMail();
    const user = userEvent.setup();

    expect(await screen.findByText("reviewer@corp.example")).toBeVisible();
    expect(screen.getByText("실패")).toBeVisible();
    expect(
      screen.getByText("SMTP 연결 실패: connection refused"),
    ).toBeVisible();

    await user.type(
      screen.getByLabelText("시험 발송 받는 사람"),
      "me@corp.example",
    );
    await user.click(screen.getByRole("button", { name: /시험 발송/ }));
    await waitFor(() =>
      expect(
        calls.some(
          (call) =>
            call.method === "POST" && call.url.endsWith("/admin/mail/test"),
        ),
      ).toBe(true),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "me@corp.example 로 보냈습니다",
    );
  });
});
