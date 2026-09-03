import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  AppWindow,
  Blocks,
  Bot,
  Boxes,
  ChevronDown,
  ClipboardCheck,
  CodeXml,
  ExternalLink,
  FolderHeart,
  Gauge,
  KeyRound,
  LayoutGrid,
  ListChecks,
  LogIn,
  LogOut,
  Menu,
  Moon,
  Network,
  Plus,
  Search,
  Settings,
  Shield,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Sun,
  Tags,
  UserRound,
  Users,
  Workflow,
  X,
  type LucideIcon,
} from "lucide-react";
import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type PropsWithChildren,
} from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { hasAnyRole, initials } from "../lib/utils";
import { useAuth, useTheme } from "../app/providers";
import { Button, ButtonLink, ErrorState } from "./ui";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

// Query parameters that give a path its own nav entry.
const NAV_VARIANT_PARAMS = ["mcp"];

/**
 * Marks a nav entry active from the whole URL, query string included.
 *
 * NavLink's own `isActive` only looks at the pathname, so `/apps` and
 * `/apps?mcp=true` would light up together. Every query parameter an entry
 * declares must match, and an entry that declares none must not match a URL
 * that carries one of its siblings' parameters.
 */
export function navItemActive(
  to: string,
  pathname: string,
  search: string,
  end = false,
): boolean {
  const [targetPath = "", targetSearch = ""] = to.split("?");
  const current = new URLSearchParams(search);
  const target = new URLSearchParams(targetSearch);
  const pathMatches =
    end || targetSearch
      ? pathname === targetPath
      : pathname === targetPath || pathname.startsWith(`${targetPath}/`);
  if (!pathMatches) return false;
  if (targetSearch)
    return [...target].every(([key, value]) => current.get(key) === value);
  return !NAV_VARIANT_PARAMS.some((key) => current.has(key));
}

interface NavGroup {
  label: string;
  items: NavItem[];
}

const publicGroups: NavGroup[] = [
  {
    label: "스토어",
    items: [
      { to: "/", label: "투데이", icon: Sparkles, end: true },
      { to: "/apps", label: "전체 앱", icon: LayoutGrid },
      { to: "/categories", label: "카테고리", icon: Tags },
      { to: "/apps?mcp=true", label: "MCP 앱", icon: Blocks },
      { to: "/favorites", label: "즐겨찾기", icon: FolderHeart },
    ],
  },
];

const personalGroup: NavGroup = {
  label: "개인",
  items: [
    { to: "/my", label: "내 홈", icon: Gauge, end: true },
    { to: "/my/apps", label: "내 앱", icon: AppWindow },
    { to: "/my/keys", label: "API · MCP 키", icon: KeyRound },
    { to: "/my/profile", label: "프로필", icon: UserRound },
    { to: "/my/activity", label: "활동 내역", icon: Activity },
    { to: "/my/settings", label: "설정", icon: Settings },
  ],
};

const adminGroups: NavGroup[] = [
  {
    label: "운영",
    items: [
      { to: "/admin", label: "대시보드", icon: Gauge, end: true },
      { to: "/admin/apps", label: "앱 관리", icon: AppWindow },
      { to: "/admin/categories", label: "카테고리", icon: Tags },
      { to: "/admin/users", label: "사용자", icon: Users },
      { to: "/admin/roles", label: "역할·권한", icon: ShieldCheck },
      { to: "/admin/reviews", label: "검토 관리", icon: ClipboardCheck },
      { to: "/admin/audit", label: "감사 로그", icon: Activity },
    ],
  },
  {
    label: "플랫폼",
    items: [
      { to: "/admin/workflow", label: "승인 워크플로", icon: Workflow },
      { to: "/admin/ai", label: "AI 공급자", icon: Bot },
      { to: "/admin/api", label: "REST API", icon: CodeXml },
      { to: "/admin/mcp", label: "MCP 서버", icon: Network },
      { to: "/admin/api-keys", label: "API 키", icon: KeyRound },
      { to: "/admin/authentication", label: "인증·SSO", icon: Shield },
      { to: "/admin/security", label: "보안·키 정책", icon: SlidersHorizontal },
      { to: "/admin/settings", label: "시스템 설정", icon: Settings },
    ],
  },
];

function ProfileMenu() {
  const { session, logout } = useAuth();
  const version = useQuery({
    queryKey: ["version"],
    queryFn: ({ signal }) => api.version(signal),
    staleTime: Infinity,
  });
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  useEffect(() => {
    if (!open) return;
    const close = (event: PointerEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  if (!session?.authenticated || !session.user) {
    return (
      <ButtonLink to="/login" variant="secondary">
        <LogIn size={18} /> 로그인
      </ButtonLink>
    );
  }

  const user = session.user;
  const label = user.displayName || user.username;
  return (
    <div className="profile-wrap" ref={wrapRef}>
      <Button
        variant="secondary"
        className="profile-trigger"
        aria-expanded={open}
        aria-haspopup="menu"
        onClick={() => setOpen((value) => !value)}
      >
        <span className="avatar" aria-hidden="true">
          {initials(label)}
        </span>
        <span>{label}</span>
        <ChevronDown size={16} />
      </Button>
      {open && (
        <div className="profile-menu" role="menu" aria-label="프로필 메뉴">
          <div className="profile-summary">
            <strong>{label}</strong>
            <span>{user.team || user.email || "AppStore 사용자"}</span>
          </div>
          <Link
            role="menuitem"
            className="menu-item"
            to="/my/apps"
            onClick={() => setOpen(false)}
          >
            <AppWindow size={18} /> 내 앱
          </Link>
          <Link
            role="menuitem"
            className="menu-item"
            to="/my/keys"
            onClick={() => setOpen(false)}
          >
            <KeyRound size={18} /> API · MCP 키
          </Link>
          <Link
            role="menuitem"
            className="menu-item"
            to="/my/settings"
            onClick={() => setOpen(false)}
          >
            <Settings size={18} /> 개인 설정
          </Link>
          <button
            role="menuitem"
            className="menu-item"
            onClick={async () => {
              await logout();
              setOpen(false);
              navigate("/");
            }}
          >
            <LogOut size={18} /> 로그아웃
          </button>
          <div className="menu-item menu-version" role="none">
            <Boxes size={18} /> AppStore{" "}
            {version.data?.version ?? "버전 확인 중"}
          </div>
        </div>
      )}
    </div>
  );
}

function Sidebar({
  admin,
  open,
  close,
}: {
  admin: boolean;
  open: boolean;
  close: () => void;
}) {
  const { session } = useAuth();
  const location = useLocation();
  const config = useQuery({
    queryKey: ["public-config"],
    queryFn: ({ signal }) => api.publicConfig(signal),
    staleTime: 60_000,
  });
  const roles = session?.user?.roles;
  const groups = admin
    ? adminGroups
    : [
        ...publicGroups,
        ...(session?.authenticated ? [personalGroup] : []),
        ...(hasAnyRole(roles, [
          "reviewer",
          "team_leader",
          "admin",
          "super_admin",
        ])
          ? [
              {
                label: "검토",
                items: [
                  { to: "/review", label: "검토 대기", icon: ListChecks },
                ],
              },
            ]
          : []),
        ...(hasAnyRole(roles, ["admin", "super_admin"])
          ? [
              {
                label: "관리",
                items: [
                  { to: "/admin", label: "관리자 콘솔", icon: ShieldCheck },
                ],
              },
            ]
          : []),
      ];

  return (
    <aside
      className={`sidebar${open ? " open" : ""}`}
      aria-label={admin ? "관리자 메뉴" : "주 메뉴"}
    >
      <Link to={admin ? "/admin" : "/"} className="brand" onClick={close}>
        <span className="brand-mark" aria-hidden="true">
          A
        </span>
        <span>
          <span className="brand-name">
            {config.data?.siteName || "AppStore"}
          </span>
          <span className="brand-tagline">
            {admin ? "관리자 콘솔" : "사내 앱 스토어"}
          </span>
        </span>
      </Link>
      <nav className="sidebar-scroll">
        {groups.map((group) => (
          <section
            className="nav-group"
            key={group.label}
            aria-labelledby={`nav-${group.label}`}
          >
            <h2 className="nav-heading" id={`nav-${group.label}`}>
              {group.label}
            </h2>
            {group.items.map((item) => {
              const Icon = item.icon;
              const isActive = navItemActive(
                item.to,
                location.pathname,
                location.search,
                item.end,
              );
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.end}
                  onClick={close}
                  aria-current={isActive ? "page" : undefined}
                  // The callback form keeps NavLink from appending its own
                  // pathname-only "active" class on top of ours.
                  className={() => `nav-link${isActive ? " active" : ""}`}
                >
                  <Icon aria-hidden="true" />
                  <span>{item.label}</span>
                </NavLink>
              );
            })}
          </section>
        ))}
      </nav>
      {!admin && (
        <div className="sidebar-bottom">
          <ButtonLink to="/submit" className="w-full">
            <Plus size={18} /> 앱 등록
          </ButtonLink>
          {config.data?.siteUrl && (
            <a
              className="service-url"
              href={config.data.siteUrl}
              target="_blank"
              rel="noopener noreferrer"
              title={config.data.siteUrl}
            >
              <ExternalLink size={15} /> 공식 서비스 URL
            </a>
          )}
        </div>
      )}
      {admin && (
        <div className="sidebar-bottom">
          <ButtonLink to="/" variant="secondary" className="w-full">
            스토어로 이동
          </ButtonLink>
        </div>
      )}
    </aside>
  );
}

export function AppShell({
  children,
  admin = false,
}: PropsWithChildren<{ admin?: boolean }>) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const { toggle, resolved } = useTheme();
  const location = useLocation();
  const navigate = useNavigate();
  const [query, setQuery] = useState(
    new URLSearchParams(location.search).get("q") ?? "",
  );
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => setMobileOpen(false), [location.pathname]);
  useEffect(() => {
    setQuery(new URLSearchParams(location.search).get("q") ?? "");
  }, [location.search]);
  useEffect(() => {
    if (admin) return;
    const focusSearch = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (
        event.key !== "/" ||
        target?.matches("input, textarea, select, [contenteditable='true']")
      )
        return;
      event.preventDefault();
      searchRef.current?.focus();
    };
    document.addEventListener("keydown", focusSearch);
    return () => document.removeEventListener("keydown", focusSearch);
  }, [admin]);

  const search = (event: FormEvent) => {
    event.preventDefault();
    const params = new URLSearchParams();
    if (query.trim()) params.set("q", query.trim());
    navigate(`/apps${params.size ? `?${params}` : ""}`);
  };

  return (
    <div className="app-layout">
      <a href="#main-content" className="skip-link">
        본문으로 건너뛰기
      </a>
      {mobileOpen && (
        <button
          className="sidebar-overlay"
          aria-label="메뉴 닫기"
          onClick={() => setMobileOpen(false)}
        />
      )}
      <Sidebar
        admin={admin}
        open={mobileOpen}
        close={() => setMobileOpen(false)}
      />
      <div className="main-column">
        <header className="topbar">
          <Button
            variant="ghost"
            className="button-icon mobile-menu-button"
            aria-label={mobileOpen ? "메뉴 닫기" : "메뉴 열기"}
            aria-expanded={mobileOpen}
            onClick={() => setMobileOpen((value) => !value)}
          >
            {mobileOpen ? <X /> : <Menu />}
          </Button>
          {!admin && (
            <form className="topbar-search" role="search" onSubmit={search}>
              <Search aria-hidden="true" />
              <label className="sr-only" htmlFor="global-search">
                앱 검색
              </label>
              <input
                ref={searchRef}
                id="global-search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="앱 이름, 설명, 태그 검색"
              />
              <kbd aria-hidden="true">/</kbd>
            </form>
          )}
          {admin && <strong>서비스 관리자</strong>}
          <div className="top-actions">
            <Button
              variant="secondary"
              className="button-icon"
              aria-label={`${resolved === "dark" ? "라이트" : "다크"} 모드로 전환`}
              onClick={toggle}
            >
              {resolved === "dark" ? <Sun size={19} /> : <Moon size={19} />}
            </Button>
            <ProfileMenu />
          </div>
        </header>
        <main id="main-content" tabIndex={-1}>
          {children}
        </main>
      </div>
    </div>
  );
}

export function ShellConfigError({
  error,
  retry,
}: {
  error: unknown;
  retry: () => void;
}) {
  return (
    <div className="page">
      <ErrorState error={error} retry={retry} />
    </div>
  );
}
