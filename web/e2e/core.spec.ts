import { expect, test } from "@playwright/test";
import { installMockApi } from "./mock-api";

test("공개 탐색과 URL 검색 상태는 새로고침 후에도 유지된다", async ({
  page,
}) => {
  await installMockApi(page);
  await page.goto("/apps?q=agent&category=ai&sort=trending");
  await expect(page.getByRole("heading", { name: "모든 앱" })).toBeVisible();
  await expect(page.locator("#catalog-search")).toHaveValue("agent");
  await expect(page.getByLabel("카테고리")).toHaveValue("ai");
  await expect(page.getByLabel("정렬")).toHaveValue("trending");
  await expect(page.getByRole("heading", { name: "Agent Hub" })).toBeVisible();
  await page.reload();
  await expect(page).toHaveURL(/q=agent&category=ai&sort=trending/);
  await expect(page.locator("#catalog-search")).toHaveValue("agent");
});

test("비로그인 등록 접근은 원래 URL을 보존한 로그인 화면으로 이동한다", async ({
  page,
}) => {
  await installMockApi(page);
  await page.goto("/submit");
  await expect(page).toHaveURL(/\/login\?returnTo=%2Fsubmit/);
  await expect(
    page.getByRole("heading", { name: "AppStore 로그인" }),
  ).toBeVisible();
  await expect(page.getByText("AppStore v2.0.0")).toBeVisible();
});

test("일반 사용자는 관리자 화면에서 403 상태를 본다", async ({ page }) => {
  await installMockApi(page, {
    authenticated: true,
    roles: ["user", "contributor"],
  });
  await page.goto("/admin");
  await expect(
    page.getByRole("heading", { name: "접근 권한이 없습니다" }),
  ).toBeVisible();
});

test("관리자 사용자 목록은 가상화되고 route refresh를 견딘다", async ({
  page,
}) => {
  await installMockApi(page, { authenticated: true });
  await page.goto("/admin/users?q=developer");
  await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();
  await expect(page.getByText("150명")).toBeVisible();
  await expect(page.getByRole("list", { name: "사용자 목록" })).toBeVisible();
  await page.reload();
  await expect(page).toHaveURL(/\/admin\/users\?q=developer/);
  await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();
});

test("AI는 256K 설정과 SSE token streaming을 처리한다", async ({ page }) => {
  await installMockApi(page, { authenticated: true });
  await page.goto("/admin/ai");
  const contextWindow = page.getByLabel("Context Window").first();
  await expect(contextWindow).toHaveValue("262144");
  await contextWindow.fill("262144");
  await page.getByLabel("Model", { exact: true }).selectOption("compact-model");
  await page.locator("#stream-max").fill("9999");
  await expect(page.locator("#stream-max")).toHaveValue("4096");
  await page.getByRole("button", { name: "Streaming 시작" }).click();
  await expect(page.getByText("스트리밍 응답", { exact: true })).toBeVisible();
  await expect(page.getByText(/totalTokens/)).toBeVisible();
});

test("관리자는 Key Permission과 Template을 편집할 수 있다", async ({
  page,
}) => {
  await installMockApi(page, { authenticated: true });
  await page.goto("/admin/security");
  await expect(
    page.getByRole("heading", { name: "Key Permission Definitions" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "편집" }).first().click();
  await page.getByLabel("표시 이름").fill("앱 카탈로그 읽기");
  await page.getByRole("dialog").getByRole("button", { name: "저장" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();

  await page.getByRole("button", { name: "Template 추가" }).click();
  await page.getByLabel("이름").fill("AI Client");
  await page.getByLabel("AI 사용").check();
  await page.getByRole("dialog").getByRole("button", { name: "저장" }).click();
  await expect(page.getByRole("dialog")).not.toBeVisible();
});

test("light/dark 테마 선택은 새로고침 후 유지된다", async ({ page }) => {
  await installMockApi(page);
  await page.goto("/");
  await page.evaluate(() => localStorage.setItem("appstore.theme", "light"));
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "다크 모드로 전환" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});

test("모바일 메뉴는 키보드와 명시적 버튼으로 접근 가능하다", async ({
  page,
}, testInfo) => {
  test.skip(testInfo.project.name !== "mobile", "모바일 프로젝트에서만 실행");
  await installMockApi(page);
  await page.goto("/");
  const menu = page.getByRole("button", { name: "메뉴 열기" });
  await menu.click();
  await expect(
    page.getByRole("complementary", { name: "주 메뉴" }),
  ).toHaveClass(/open/);
  await expect(page.getByRole("link", { name: /MCP Apps/ })).toBeVisible();
});

test("관리자는 앱 상세를 수정하고 삭제 확인 후 목록으로 돌아온다", async ({
  page,
}) => {
  await installMockApi(page, { authenticated: true });
  await page.goto("/admin/apps");
  await page.getByRole("link", { name: "Agent Hub" }).click();
  await expect(page).toHaveURL(
    "/admin/apps/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  );
  await expect(page.getByLabel("앱 이름")).toHaveValue("Agent Hub");
  await page.getByLabel("게시 상태").selectOption("archived");
  await page.getByRole("button", { name: "변경 저장" }).click();
  await expect(page.getByText("앱 정보가 저장되었습니다.")).toBeVisible();

  await page.getByRole("button", { name: "앱 삭제" }).click();
  const dialog = page.getByRole("dialog", { name: "앱을 영구 삭제할까요?" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "영구 삭제" }).click();
  await expect(page).toHaveURL("/admin/apps");
});

test("SSO 연결 테스트는 discovery endpoint를 표시한다", async ({ page }) => {
  await installMockApi(page, { authenticated: true });
  await page.goto("/admin/authentication");
  await page.getByRole("button", { name: "SSO 연결 테스트" }).click();
  await expect(page.getByText("discovery 문서를 정상적으로")).toBeVisible();
  await expect(
    page.getByText(
      "https://sso.example.internal/realms/company/protocol/openid-connect/token",
    ),
  ).toBeVisible();
  await expect(page.getByText("지원", { exact: true })).toBeVisible();
});

test("스토어 메뉴는 Apps와 MCP Apps를 동시에 선택하지 않는다", async ({
  page,
}) => {
  await installMockApi(page);
  const menu = page.getByRole("navigation").or(page.locator(".sidebar-scroll"));
  await page.goto("/apps");
  await expect(
    menu.getByRole("link", { name: "Apps", exact: true }),
  ).toHaveClass(/active/);
  await expect(menu.getByRole("link", { name: "MCP Apps" })).not.toHaveClass(
    /active/,
  );
  await page.goto("/apps?mcp=true");
  await expect(menu.getByRole("link", { name: "MCP Apps" })).toHaveClass(
    /active/,
  );
  await expect(
    menu.getByRole("link", { name: "Apps", exact: true }),
  ).not.toHaveClass(/active/);
});

test("SSO를 설정해도 복구용 관리자 로그인을 계속 사용할 수 있다", async ({
  page,
}) => {
  await installMockApi(page);
  await page.goto("/login");
  await expect(
    page.getByRole("link", { name: "회사 계정으로 SSO 로그인" }),
  ).toBeVisible();

  const toggle = page.getByRole("button", { name: "관리자 계정으로 로그인" });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByLabel("Bootstrap 관리자")).toBeHidden();

  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByLabel("Bootstrap 관리자")).toBeVisible();
  await expect(page.getByLabel("비밀번호")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "관리자 로그인" }),
  ).toBeVisible();
});
