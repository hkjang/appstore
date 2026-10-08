import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
  AppGuideDocuments,
  GuideDocumentsField,
  guideDocumentError,
  useGuideDocumentDraft,
} from "./guide-documents";

const storedDocument = {
  id: "11111111-1111-4111-8111-111111111111",
  appId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  title: "운영 가이드",
  fileName: "운영 가이드.pdf",
  contentType: "application/pdf",
  size: 2048,
  createdAt: "2026-09-01T08:00:00Z",
  downloadUrl:
    "/api/v1/apps/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/documents/11111111-1111-4111-8111-111111111111",
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function stubFetch(items: unknown[]) {
  const fetchMock = vi
    .fn()
    .mockImplementation((path: string, init?: RequestInit) => {
      if ((init?.method ?? "GET") === "GET")
        return Promise.resolve(jsonResponse({ items }));
      if (init?.method === "DELETE")
        return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(storedDocument, 201));
    });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderWithClient(ui: React.ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

// The form stages every change and replays it once the app has an identifier,
// which is the only order that works on the register screen.
function DraftHarness({ appId }: { appId?: string }) {
  const draft = useGuideDocumentDraft(appId);
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void draft.apply(appId ?? "new-app-id");
      }}
    >
      <GuideDocumentsField draft={draft} />
      <button type="submit">저장</button>
    </form>
  );
}

describe("guide document rules", () => {
  it("refuses an extension, an empty file, an oversized file and a full app", () => {
    const pdf = new File(["guide"], "가이드.pdf", { type: "application/pdf" });
    expect(guideDocumentError(pdf, 0)).toBe("");
    expect(guideDocumentError(new File(["x"], "setup.exe"), 0)).toMatch(
      /첨부할 수 있습니다/,
    );
    expect(guideDocumentError(new File([], "빈.pdf"), 0)).toMatch(/빈 파일/);
    const huge = new File(["x"], "huge.pdf");
    Object.defineProperty(huge, "size", { value: 21 * 1024 * 1024 });
    expect(guideDocumentError(huge, 0)).toMatch(/20MB/);
    expect(guideDocumentError(pdf, 10)).toMatch(/10개까지/);
  });
});

describe("guide documents on an app form", () => {
  it("summarizes rejected files with the first reason in one alert", async () => {
    const fetchMock = stubFetch([]);
    const user = userEvent.setup();
    renderWithClient(<DraftHarness />);

    await user.upload(screen.getByLabelText("가이드 문서 파일 선택"), [
      new File([], "첫째.pdf", { type: "application/pdf" }),
      new File([], "둘째.pdf", { type: "application/pdf" }),
    ]);

    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByRole("alert").textContent).toBe(
      "첫째.pdf: 빈 파일은 첨부할 수 없습니다. (총 2개 파일 첨부 실패)",
    );
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("keeps stored and pending files and stages mixed picks in selection order", async () => {
    const fetchMock = stubFetch([storedDocument]);
    const user = userEvent.setup();
    renderWithClient(<DraftHarness appId={storedDocument.appId} />);
    expect(await screen.findByText("운영 가이드")).toBeInTheDocument();
    const input = screen.getByLabelText("가이드 문서 파일 선택");
    await user.upload(input, new File(["guide"], "이전.pdf"));
    await user.upload(input, [
      new File([], "첫째.pdf"),
      new File(["guide"], "정상1.pdf"),
      new File([], "둘째.pdf"),
      new File(["guide"], "정상2.pdf"),
    ]);

    expect(screen.getByRole("alert").textContent).toBe(
      "첫째.pdf: 빈 파일은 첨부할 수 없습니다. (총 2개 파일 첨부 실패)",
    );
    expect(
      screen
        .getAllByRole("listitem")
        .map((row) => row.querySelector("strong")?.textContent),
    ).toEqual(["운영 가이드", "이전.pdf", "정상1.pdf", "정상2.pdf"]);
    expect(screen.getAllByText(/저장 시 업로드됩니다/)).toHaveLength(3);
    expect(
      fetchMock.mock.calls.filter(
        ([, init]) => (init?.method ?? "GET") !== "GET",
      ),
    ).toHaveLength(0);
  });

  it("clears the summary on a valid pick and keeps later single failures unchanged", async () => {
    stubFetch([]);
    const user = userEvent.setup();
    renderWithClient(<DraftHarness />);
    const input = screen.getByLabelText("가이드 문서 파일 선택");
    await user.upload(input, [
      new File([], "첫째.pdf"),
      new File([], "둘째.pdf"),
    ]);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "총 2개 파일 첨부 실패",
    );

    await user.upload(input, new File(["guide"], "정상.pdf"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("정상.pdf")).toBeInTheDocument();

    await user.upload(input, new File([], "단일.pdf"));
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByRole("alert").textContent).toBe(
      "단일.pdf: 빈 파일은 첨부할 수 없습니다.",
    );
    expect(screen.getByText("정상.pdf")).toBeInTheDocument();
  });

  it("counts duplicate names once per rejected file across stored, pending and current picks", async () => {
    stubFetch([storedDocument]);
    const user = userEvent.setup();
    renderWithClient(<DraftHarness appId={storedDocument.appId} />);
    expect(await screen.findByText("운영 가이드")).toBeInTheDocument();
    const input = screen.getByLabelText("가이드 문서 파일 선택");
    await user.upload(input, new File(["guide"], "pending.pdf"));
    await user.upload(input, [
      new File(["guide"], "운영 가이드.PDF"),
      new File(["guide"], "PENDING.pdf"),
      new File(["guide"], "new.pdf"),
      new File(["guide"], "NEW.pdf"),
    ]);

    expect(screen.getByRole("alert").textContent).toBe(
      "운영 가이드.PDF: 같은 이름의 문서가 이미 있습니다. (총 3개 파일 첨부 실패)",
    );
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
    expect(screen.getByText("new.pdf")).toBeInTheDocument();
  });

  it("counts each file over the attachment limit without dropping accepted files", async () => {
    stubFetch([]);
    const user = userEvent.setup();
    renderWithClient(<DraftHarness />);
    const input = screen.getByLabelText("가이드 문서 파일 선택");
    await user.upload(
      input,
      Array.from(
        { length: 9 },
        (_, index) => new File(["guide"], `${index}.pdf`),
      ),
    );
    await user.upload(input, [
      new File(["guide"], "마지막.pdf"),
      new File(["guide"], "초과1.pdf"),
      new File(["guide"], "초과2.pdf"),
    ]);

    expect(screen.getByRole("alert").textContent).toBe(
      "가이드 문서는 앱당 10개까지 첨부할 수 있습니다. (총 2개 파일 첨부 실패)",
    );
    expect(screen.getAllByRole("listitem")).toHaveLength(10);
    expect(screen.getByText("마지막.pdf")).toBeInTheDocument();
    expect(screen.queryByText("초과1.pdf")).not.toBeInTheDocument();
    expect(screen.queryByText("초과2.pdf")).not.toBeInTheDocument();
  });

  it("stages a picked file and a removal until the form is saved", async () => {
    const fetchMock = stubFetch([storedDocument]);
    const user = userEvent.setup();
    renderWithClient(
      <DraftHarness appId="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" />,
    );

    expect(await screen.findByText("운영 가이드")).toBeInTheDocument();
    await user.upload(
      screen.getByLabelText("가이드 문서 파일 선택"),
      new File(["설치"], "설치 안내.pdf", { type: "application/pdf" }),
    );
    expect(await screen.findByText("설치 안내.pdf")).toBeInTheDocument();
    expect(screen.getByText(/저장 시 업로드됩니다/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /삭제/ }));
    expect(screen.getByText(/저장 시 삭제됩니다/)).toBeInTheDocument();
    // Nothing has been written yet: only the initial listing was requested.
    expect(
      fetchMock.mock.calls.filter(
        ([, init]) => (init?.method ?? "GET") !== "GET",
      ),
    ).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "저장" }));
    await waitFor(() => {
      const writes = fetchMock.mock.calls.filter(
        ([, init]) => (init?.method ?? "GET") !== "GET",
      );
      expect(writes.map(([, init]) => init?.method)).toEqual([
        "DELETE",
        "POST",
      ]);
    });
    const upload = fetchMock.mock.calls.find(
      ([, init]) => init?.method === "POST",
    );
    expect(upload?.[0]).toBe(
      "/api/v1/apps/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/documents",
    );
    expect((upload?.[1]?.body as FormData).get("file")).toBeInstanceOf(File);
  });

  it("refuses a name the app already carries", async () => {
    stubFetch([storedDocument]);
    const user = userEvent.setup();
    renderWithClient(
      <DraftHarness appId="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" />,
    );
    expect(await screen.findByText("운영 가이드")).toBeInTheDocument();
    await user.upload(
      screen.getByLabelText("가이드 문서 파일 선택"),
      new File(["again"], storedDocument.fileName, {
        type: "application/pdf",
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /같은 이름의 문서가 이미 있습니다/,
    );
    expect(screen.queryByText(/저장 시 업로드됩니다/)).not.toBeInTheDocument();
  });

  it("keeps an oversized pick off the wire and explains why", async () => {
    const fetchMock = stubFetch([]);
    const user = userEvent.setup();
    renderWithClient(<DraftHarness />);
    const huge = new File(["guide"], "대용량 가이드.pdf", {
      type: "application/pdf",
    });
    Object.defineProperty(huge, "size", { value: 21 * 1024 * 1024 });
    await user.upload(screen.getByLabelText("가이드 문서 파일 선택"), huge);
    expect(await screen.findByRole("alert")).toHaveTextContent(/20MB/);
    expect(screen.queryByText("대용량 가이드.pdf")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("guide documents on the app detail page", () => {
  it("offers every attachment as a download without a session", async () => {
    stubFetch([storedDocument]);
    renderWithClient(<AppGuideDocuments appSlug="agent-hub" />);
    const link = await screen.findByRole("link", { name: /내려받기/ });
    expect(link).toHaveAttribute("href", storedDocument.downloadUrl);
    expect(link).toHaveAttribute("download", storedDocument.fileName);
  });

  it("stays away when an app has nothing attached", async () => {
    stubFetch([]);
    renderWithClient(<AppGuideDocuments appSlug="agent-hub" />);
    await waitFor(() =>
      expect(screen.queryByText("가이드 문서")).not.toBeInTheDocument(),
    );
  });
});
