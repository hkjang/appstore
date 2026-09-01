import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  Activity,
  AppWindow,
  Bot,
  CheckCircle2,
  ClipboardCheck,
  Plus,
  Play,
  RefreshCw,
  Save,
  Search,
  Settings,
  ShieldCheck,
  Square,
  Trash2,
  Users,
  Workflow,
} from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, streamAiChat } from "../lib/api";
import { clampToken, formatDateTime } from "../lib/utils";
import type {
  AiModelLimit,
  AuditEntry,
  Category,
  KeyPermissionDefinition,
  KeyPermissionTemplate,
  PersonalKey,
  StoreApp,
  User,
} from "../types";
import {
  Badge,
  Button,
  Card,
  Dialog,
  EmptyState,
  ErrorState,
  Field,
  Input,
  LoadingState,
  PageHeader,
  Select,
  Switch,
  Textarea,
} from "../components/ui";
import { ReviewQueuePage } from "./review-pages";

type SettingsRecord = Record<string, unknown>;

function recordFrom(value: unknown): SettingsRecord {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const record = value as SettingsRecord;
  return record.settings &&
    typeof record.settings === "object" &&
    !Array.isArray(record.settings)
    ? (record.settings as SettingsRecord)
    : record;
}

function arrayFrom<T>(value: unknown, keys: string[]): T[] {
  if (Array.isArray(value)) return value as T[];
  const record = recordFrom(value);
  for (const key of ["items", ...keys])
    if (Array.isArray(record[key])) return record[key] as T[];
  return [];
}

function text(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}
function number(value: unknown, fallback = 0): number {
  return typeof value === "number" ? value : Number(value) || fallback;
}
function bool(value: unknown, fallback = false): boolean {
  return typeof value === "boolean" ? value : fallback;
}

const ROLE_MAPPING_FIELDS: ReadonlyArray<readonly [string, string]> = [
  ["user", "User"],
  ["contributor", "Contributor"],
  ["reviewer", "Reviewer"],
  ["team_leader", "Team Leader"],
  ["admin", "Admin"],
  ["super_admin", "Super Admin"],
];

function externalRoleFor(value: unknown, internalRole: string): string {
  const mappings = recordFrom(value);
  return (
    Object.entries(mappings).find(
      ([, roles]) => Array.isArray(roles) && roles.includes(internalRole),
    )?.[0] ?? ""
  );
}

function updateRoleMapping(
  value: unknown,
  internalRole: string,
  externalRole: string,
): Record<string, string[]> {
  const next: Record<string, string[]> = {};
  for (const [external, roles] of Object.entries(recordFrom(value))) {
    if (!Array.isArray(roles)) continue;
    const remaining = roles.filter(
      (role): role is string =>
        typeof role === "string" && role !== internalRole,
    );
    if (remaining.length) next[external] = remaining;
  }
  if (externalRole.trim()) {
    const key = externalRole.trim();
    next[key] = [...new Set([...(next[key] ?? []), internalRole])];
  }
  return next;
}

function useAdminSettings(resource: string, defaults: SettingsRecord) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ["admin", resource],
    queryFn: ({ signal }) => api.admin<unknown>(resource, signal),
  });
  const [settings, setSettings] = useState<SettingsRecord>(defaults);
  useEffect(() => {
    if (query.data) setSettings({ ...defaults, ...recordFrom(query.data) });
  }, [query.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useMutation({
    mutationFn: (input: SettingsRecord) =>
      api.updateAdmin<SettingsRecord>(resource, input),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["admin", resource] });
    },
  });
  const set = (key: string, value: unknown) =>
    setSettings((current) => ({ ...current, [key]: value }));
  return { query, settings, set, save };
}

export function AdminDashboardPage() {
  const dashboard = useQuery({
    queryKey: ["admin", "dashboard"],
    queryFn: ({ signal }) => api.admin<unknown>("dashboard", signal),
  });
  const values = recordFrom(dashboard.data);
  return (
    <div className="page">
      <PageHeader
        eyebrow="Administration"
        title="Dashboard"
        description="AppStore 운영 상태와 검토·보안 항목을 한눈에 확인합니다."
      />
      {dashboard.isPending && <LoadingState />}
      {dashboard.error && (
        <ErrorState
          error={dashboard.error}
          retry={() => void dashboard.refetch()}
        />
      )}
      {!!dashboard.data && (
        <>
          <div className="stats-grid">
            <Stat
              label="전체 앱"
              value={number(values.appsTotal ?? values.apps_total)}
              icon={<AppWindow />}
            />
            <Stat
              label="게시 앱"
              value={number(values.appsPublished ?? values.apps_published)}
              icon={<CheckCircle2 />}
            />
            <Stat
              label="검토 대기"
              value={number(values.reviewsPending ?? values.reviews_pending)}
              icon={<ClipboardCheck />}
            />
            <Stat
              label="활성 사용자"
              value={number(values.usersActive ?? values.users_active)}
              icon={<Users />}
            />
          </div>
          <div className="detail-body">
            <Card className="prose-card">
              <h2>운영 바로가기</h2>
              <div className="grid gap-2">
                <Link className="menu-item" to="/admin/authentication">
                  <ShieldCheck size={19} /> Keycloak OIDC 설정
                </Link>
                <Link className="menu-item" to="/admin/workflow">
                  <Workflow size={19} /> 승인 Workflow
                </Link>
                <Link className="menu-item" to="/admin/ai">
                  <Bot size={19} /> AI Provider
                </Link>
                <Link className="menu-item" to="/admin/settings">
                  <Settings size={19} /> 서비스 URL 및 시스템 설정
                </Link>
              </div>
            </Card>
            <Card className="prose-card">
              <h2>보안 상태</h2>
              <dl className="meta-list">
                <Meta
                  label="OIDC"
                  value={
                    bool(values.oidcConfigured ?? values.oidc_configured)
                      ? "연결됨"
                      : "설정 필요"
                  }
                />
                <Meta
                  label="Workflow"
                  value={
                    bool(values.workflowEnabled ?? values.workflow_enabled)
                      ? "사용"
                      : "미사용"
                  }
                />
                <Meta
                  label="AI Streaming"
                  value={
                    bool(values.aiStreaming ?? values.ai_streaming, true)
                      ? "기본 ON"
                      : "OFF"
                  }
                />
                <Meta label="감사 로그" value="삭제 불가" />
              </dl>
            </Card>
          </div>
        </>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  icon,
}: {
  label: string;
  value: number;
  icon: React.ReactNode;
}) {
  return (
    <Card className="stat-card">
      <div className="flex items-center justify-between">
        <span className="stat-label">{label}</span>
        <span className="text-[var(--primary)]">{icon}</span>
      </div>
      <strong className="stat-value">{value.toLocaleString()}</strong>
    </Card>
  );
}
function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="meta-row">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

export function AdminAppsPage() {
  const client = useQueryClient();
  const apps = useQuery({
    queryKey: ["admin", "apps"],
    queryFn: ({ signal }) => api.admin<unknown>("apps", signal),
  });
  const rows = arrayFrom<StoreApp>(apps.data, ["apps"]);
  const updateStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api.setAdminAppStatus(id, status),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["admin", "apps"] });
      await client.invalidateQueries({ queryKey: ["apps"] });
    },
  });
  return (
    <div className="page">
      <PageHeader
        eyebrow="Catalog management"
        title="Apps"
        description="등록된 모든 앱과 게시 상태를 관리합니다."
      />
      {apps.isPending && <LoadingState />}
      {apps.error && (
        <ErrorState error={apps.error} retry={() => void apps.refetch()} />
      )}
      {!!apps.data && !rows.length && <EmptyState />}
      {!!rows.length && (
        <Card className="data-card">
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>앱</th>
                  <th>담당팀</th>
                  <th>카테고리</th>
                  <th>상태</th>
                  <th>버전</th>
                  <th>업데이트</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((app) => (
                  <tr key={app.id}>
                    <td>
                      <strong>{app.name}</strong>
                      <div className="field-help">/{app.slug}</div>
                    </td>
                    <td>{app.team || "—"}</td>
                    <td>{app.category?.name || app.categoryName || "—"}</td>
                    <td>
                      <Select
                        aria-label={`${app.name} 상태`}
                        value={app.status || "draft"}
                        disabled={updateStatus.isPending}
                        onChange={(event) =>
                          updateStatus.mutate({
                            id: app.id,
                            status: event.target.value,
                          })
                        }
                      >
                        <option value="draft">draft</option>
                        <option value="pending_review">pending_review</option>
                        <option value="published">published</option>
                        <option value="rejected">rejected</option>
                        <option value="archived">archived</option>
                      </Select>
                    </td>
                    <td>{app.version || "—"}</td>
                    <td>{formatDateTime(app.updatedAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  );
}

export function AdminCategoriesPage() {
  const client = useQueryClient();
  const categories = useQuery({
    queryKey: ["admin", "categories"],
    queryFn: ({ signal }) => api.admin<unknown>("categories", signal),
  });
  const rows = arrayFrom<Category>(categories.data, ["categories"]);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Category>();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [icon, setIcon] = useState("📦");
  const [description, setDescription] = useState("");
  const [position, setPosition] = useState(0);
  const [active, setActive] = useState(true);
  const reset = () => {
    setEditing(undefined);
    setName("");
    setSlug("");
    setIcon("📦");
    setDescription("");
    setPosition(0);
    setActive(true);
  };
  const saveCategory = useMutation({
    mutationFn: () => {
      const input = { name, slug, icon, description, position, active };
      return editing
        ? api.updateAdmin<Category>(`categories/${editing.id}`, input)
        : api.adminAction<Category>("categories", input);
    },
    onSuccess: async () => {
      setOpen(false);
      reset();
      await client.invalidateQueries({ queryKey: ["admin", "categories"] });
      await client.invalidateQueries({ queryKey: ["categories"] });
    },
  });
  const deleteCategory = useMutation({
    mutationFn: (id: string) => api.deleteAdmin(`categories/${id}`),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["admin", "categories"] });
      await client.invalidateQueries({ queryKey: ["categories"] });
    },
  });
  const createNew = () => {
    reset();
    setOpen(true);
  };
  const edit = (category: Category) => {
    setEditing(category);
    setName(category.name);
    setSlug(category.slug);
    setIcon(category.icon || "📦");
    setDescription(category.description || "");
    setPosition(category.position ?? 0);
    setActive(category.active ?? true);
    setOpen(true);
  };
  return (
    <div className="page">
      <PageHeader
        eyebrow="Taxonomy"
        title="Categories"
        description="앱 탐색에 사용하는 카테고리를 관리합니다."
        actions={<Button onClick={createNew}>카테고리 추가</Button>}
      />
      {categories.isPending && <LoadingState />}
      {categories.error && (
        <ErrorState
          error={categories.error}
          retry={() => void categories.refetch()}
        />
      )}
      {!!categories.data && !rows.length && <EmptyState />}
      {!!rows.length && (
        <div className="card-grid">
          {rows.map((category) => (
            <Card className="prose-card" key={category.id}>
              <Badge>{category.appCount ?? 0}개 앱</Badge>
              <h2 className="section-title !mt-4">{category.name}</h2>
              <p>{category.description || `/${category.slug}`}</p>
              <div className="form-actions">
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => edit(category)}
                >
                  편집
                </Button>
                <Button
                  size="sm"
                  variant="danger"
                  disabled={
                    deleteCategory.isPending || (category.appCount ?? 0) > 0
                  }
                  title={
                    (category.appCount ?? 0) > 0
                      ? "사용 중인 카테고리는 삭제할 수 없습니다."
                      : undefined
                  }
                  onClick={() => {
                    if (confirm(`${category.name} 카테고리를 삭제할까요?`))
                      deleteCategory.mutate(category.id);
                  }}
                >
                  삭제
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title={editing ? "카테고리 편집" : "카테고리 추가"}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            saveCategory.mutate();
          }}
        >
          <Field label="이름" id="category-name">
            <Input
              id="category-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
            />
          </Field>
          <Field label="Slug" id="category-slug">
            <Input
              id="category-slug"
              value={slug}
              onChange={(event) =>
                setSlug(
                  event.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-"),
                )
              }
              required
            />
          </Field>
          <div className="form-grid">
            <Field label="Icon" id="category-icon">
              <Input
                id="category-icon"
                value={icon}
                onChange={(event) => setIcon(event.target.value)}
              />
            </Field>
            <Field label="정렬 순서" id="category-position">
              <Input
                id="category-position"
                type="number"
                min={0}
                value={position}
                onChange={(event) => setPosition(Number(event.target.value))}
              />
            </Field>
          </div>
          <Field label="설명" id="category-description">
            <Textarea
              id="category-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </Field>
          <div className="switch-row">
            <strong>활성 카테고리</strong>
            <Switch
              checked={active}
              onChange={setActive}
              label="활성 카테고리"
            />
          </div>
          {saveCategory.error && (
            <p className="field-error">{saveCategory.error.message}</p>
          )}
          <div className="form-actions">
            <Button variant="secondary" onClick={() => setOpen(false)}>
              취소
            </Button>
            <Button type="submit" disabled={saveCategory.isPending}>
              {editing ? "저장" : "추가"}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}

export function AdminUsersPage() {
  const client = useQueryClient();
  const [params, setParams] = useSearchParams();
  const queryText = params.get("q") ?? "";
  const [draft, setDraft] = useState(queryText);
  const users = useQuery({
    queryKey: ["admin-users", queryText],
    queryFn: ({ signal }) =>
      api.adminUsers({ q: queryText, page: 1, pageSize: 1000 }, signal),
  });
  const [editing, setEditing] = useState<User>();
  const updateUser = useMutation({
    mutationFn: (user: User) =>
      api.updateAdmin<User>(`users/${user.id}`, {
        username: user.username,
        email: user.email ?? "",
        displayName: user.displayName ?? user.username,
        team: user.team ?? "",
        active: user.active ?? true,
        roles: user.roles,
      }),
    onSuccess: async () => {
      setEditing(undefined);
      await client.invalidateQueries({ queryKey: ["admin-users"] });
    },
  });
  const parentRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: users.data?.items.length ?? 0,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 70,
    overscan: 8,
    useFlushSync: false,
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setParams((current) => {
      const next = new URLSearchParams(current);
      if (draft.trim()) next.set("q", draft.trim());
      else next.delete("q");
      return next;
    });
  };
  return (
    <div className="page">
      <PageHeader
        eyebrow="Identity & access"
        title="Users"
        description="대규모 사용자 목록은 가상화되어 필요한 행만 렌더링합니다."
      />
      <Card className="data-card">
        <div className="data-toolbar">
          <form className="topbar-search !max-w-md" onSubmit={submit}>
            <Search />
            <label className="sr-only" htmlFor="user-search">
              사용자 검색
            </label>
            <input
              id="user-search"
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              placeholder="이름, 이메일, 역할"
            />
          </form>
          <Badge>{users.data?.total ?? 0}명</Badge>
        </div>
        {users.isPending && <LoadingState />}
        {users.error && (
          <ErrorState error={users.error} retry={() => void users.refetch()} />
        )}
        {users.data && !users.data.items.length && <EmptyState />}
        {!!users.data?.items.length && (
          <>
            <div
              className="virtual-row !relative !grid bg-[var(--bg-muted)] font-bold text-[13px]"
              aria-hidden="true"
            >
              <span>사용자</span>
              <span>이메일</span>
              <span>역할</span>
              <span>상태</span>
              <span>작업</span>
            </div>
            <div
              className="virtual-list"
              ref={parentRef}
              role="list"
              aria-label="사용자 목록"
            >
              <div
                className="virtual-list-inner"
                style={{ height: virtualizer.getTotalSize() }}
              >
                {virtualizer.getVirtualItems().map((virtualRow) => {
                  const user = users.data.items[virtualRow.index];
                  if (!user) return null;
                  return (
                    <div
                      role="listitem"
                      className="virtual-row"
                      key={user.id}
                      style={{
                        height: virtualRow.size,
                        transform: `translateY(${virtualRow.start}px)`,
                      }}
                    >
                      <span>
                        <strong>{user.displayName || user.username}</strong>
                        <span className="field-help block">
                          {user.team || user.username}
                        </span>
                      </span>
                      <span>{user.email || "—"}</span>
                      <span className="badge-row !mt-0">
                        {user.roles.slice(0, 2).map((role) => (
                          <Badge key={role}>{role}</Badge>
                        ))}
                      </span>
                      <span>
                        <Badge tone={user.active ? "positive" : "warning"}>
                          {user.active ? "활성" : "비활성"}
                        </Badge>
                      </span>
                      <span>
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => setEditing(user)}
                        >
                          관리
                        </Button>
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          </>
        )}
      </Card>
      <Dialog
        open={!!editing}
        onClose={() => setEditing(undefined)}
        title="사용자 관리"
        description="프로필, 활성 상태와 AppStore 역할을 변경합니다."
      >
        {editing && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              updateUser.mutate(editing);
            }}
          >
            <div className="form-grid">
              <Field label="사용자명" id="admin-user-username">
                <Input
                  id="admin-user-username"
                  value={editing.username}
                  onChange={(event) =>
                    setEditing({ ...editing, username: event.target.value })
                  }
                  required
                />
              </Field>
              <Field label="표시 이름" id="admin-user-display-name">
                <Input
                  id="admin-user-display-name"
                  value={editing.displayName ?? ""}
                  onChange={(event) =>
                    setEditing({ ...editing, displayName: event.target.value })
                  }
                  required
                />
              </Field>
              <Field label="Email" id="admin-user-email">
                <Input
                  id="admin-user-email"
                  type="email"
                  value={editing.email ?? ""}
                  onChange={(event) =>
                    setEditing({ ...editing, email: event.target.value })
                  }
                />
              </Field>
              <Field label="팀" id="admin-user-team">
                <Input
                  id="admin-user-team"
                  value={editing.team ?? ""}
                  onChange={(event) =>
                    setEditing({ ...editing, team: event.target.value })
                  }
                />
              </Field>
            </div>
            <Field
              label="역할"
              id="admin-user-roles"
              help="user, contributor, reviewer, team_leader, admin, super_admin · 쉼표로 구분"
            >
              <Input
                id="admin-user-roles"
                value={editing.roles.join(", ")}
                onChange={(event) =>
                  setEditing({
                    ...editing,
                    roles: event.target.value
                      .split(",")
                      .map((role) => role.trim().replace(/-/g, "_"))
                      .filter(Boolean),
                  })
                }
              />
            </Field>
            <div className="switch-row">
              <strong>활성 사용자</strong>
              <Switch
                checked={editing.active ?? true}
                onChange={(active) => setEditing({ ...editing, active })}
                label="활성 사용자"
              />
            </div>
            {updateUser.error && (
              <div className="notice notice-danger">
                {updateUser.error.message}
              </div>
            )}
            <div className="form-actions">
              <Button variant="secondary" onClick={() => setEditing(undefined)}>
                취소
              </Button>
              <Button type="submit" disabled={updateUser.isPending}>
                저장
              </Button>
            </div>
          </form>
        )}
      </Dialog>
    </div>
  );
}

export function AdminRolesPage() {
  const state = useAdminSettings("roles", {
    roles: [
      "user",
      "contributor",
      "reviewer",
      "team_leader",
      "admin",
      "super_admin",
    ],
    permissions: [
      "apps:read",
      "apps:write",
      "apps:submit",
      "ai:use",
      "mcp:read",
      "mcp:execute",
    ],
  });
  const [json, setJson] = useState("");
  useEffect(
    () => setJson(JSON.stringify(state.settings, null, 2)),
    [state.settings],
  );
  return (
    <div className="page">
      <PageHeader
        eyebrow="RBAC"
        title="Roles & Permissions"
        description="역할과 권한 체계 자체를 변경할 수 있습니다. 변경 전 영향 범위를 확인하세요."
      />
      {state.query.isPending && <LoadingState />}
      {state.query.error && (
        <ErrorState
          error={state.query.error}
          retry={() => void state.query.refetch()}
        />
      )}
      {!!state.query.data && (
        <Card className="form-section">
          <Field
            label="역할·권한 정의"
            id="roles-json"
            help="운영 API 계약에 맞는 JSON입니다."
          >
            <Textarea
              id="roles-json"
              className="!min-h-96 font-mono"
              value={json}
              onChange={(event) => setJson(event.target.value)}
            />
          </Field>
          {state.save.error && (
            <p className="field-error">{state.save.error.message}</p>
          )}
          <div className="form-actions">
            <Button
              disabled={state.save.isPending}
              onClick={() => {
                try {
                  state.save.mutate(JSON.parse(json) as SettingsRecord);
                } catch {
                  alert("JSON 형식을 확인하세요.");
                }
              }}
            >
              <Save size={18} /> 저장
            </Button>
          </div>
        </Card>
      )}
    </div>
  );
}

export function AdminWorkflowPage() {
  const state = useAdminSettings("workflow", {
    enabled: false,
    levels: 1,
    reviewerRoles: ["reviewer"],
    teamLeaderRoles: ["team_leader"],
    autoPublish: true,
    rejectReasonRequired: true,
    reapprovalAfterEdit: true,
    preventSelfApproval: true,
  });
  return (
    <SettingsShell
      title="Workflow"
      eyebrow="Optional approval"
      description="기본값은 OFF이며, OFF일 때 등록 앱은 검증 후 즉시 게시됩니다."
      state={state}
    >
      <SettingSwitch
        state={state}
        name="enabled"
        label="앱 등록 승인 사용"
        help={
          bool(state.settings.enabled)
            ? "등록 → 검토 대기 → 승인/반려"
            : "등록 → 즉시 게시"
        }
      />
      <div className="form-grid">
        <Field label="승인 단계" id="approval-levels">
          <Input
            id="approval-levels"
            type="number"
            min={1}
            max={10}
            value={number(state.settings.levels, 1)}
            onChange={(event) =>
              state.set("levels", Number(event.target.value))
            }
          />
        </Field>
        <Field label="Reviewer 역할" id="reviewer-role">
          <Input
            id="reviewer-role"
            value={arrayFrom<string>(state.settings.reviewerRoles, []).join(
              ", ",
            )}
            onChange={(event) =>
              state.set(
                "reviewerRoles",
                event.target.value
                  .split(",")
                  .map((role) => role.trim())
                  .filter(Boolean),
              )
            }
          />
        </Field>
        <Field label="Team Leader 역할" id="leader-role">
          <Input
            id="leader-role"
            value={arrayFrom<string>(state.settings.teamLeaderRoles, []).join(
              ", ",
            )}
            onChange={(event) =>
              state.set(
                "teamLeaderRoles",
                event.target.value
                  .split(",")
                  .map((role) => role.trim())
                  .filter(Boolean),
              )
            }
          />
        </Field>
      </div>
      <SettingSwitch
        state={state}
        name="autoPublish"
        label="승인 즉시 자동 게시"
      />
      <SettingSwitch
        state={state}
        name="rejectReasonRequired"
        label="반려 사유 필수"
      />
      <SettingSwitch
        state={state}
        name="reapprovalAfterEdit"
        label="수정 후 재승인"
      />
      <SettingSwitch
        state={state}
        name="preventSelfApproval"
        label="등록자 자체 승인 금지"
      />
    </SettingsShell>
  );
}

export function AdminAuthenticationPage() {
  const state = useAdminSettings("authentication", {
    enabled: false,
    issuerUrl: "",
    clientId: "appstore",
    roleClaimPath: "realm_access.roles",
    groupClaimPath: "",
    roleMappings: {
      "appstore-user": ["user"],
      "appstore-contributor": ["contributor"],
      "appstore-reviewer": ["reviewer"],
      "appstore-manager": ["team_leader"],
      "appstore-admin": ["admin"],
      "appstore-super-admin": ["super_admin"],
    },
    groupMappings: {},
    scopes: ["openid", "profile", "email"],
  });
  const [secret, setSecret] = useState("");
  const test = useMutation({
    mutationFn: () => api.adminAction<SettingsRecord>("authentication/test"),
  });
  return (
    <SettingsShell
      title="Keycloak OIDC"
      eyebrow="Authentication"
      description="Issuer, Client ID, Client Secret만 입력하면 discovery 문서를 통해 endpoint를 자동 구성합니다."
      state={state}
      transform={(settings) =>
        secret ? { ...settings, clientSecret: secret } : settings
      }
      extraActions={
        <Button
          variant="secondary"
          disabled={test.isPending}
          onClick={() => test.mutate()}
        >
          <RefreshCw size={17} />{" "}
          {test.isPending ? "테스트 중…" : "SSO 연결 테스트"}
        </Button>
      }
    >
      <SettingSwitch state={state} name="enabled" label="OIDC 활성화" />
      <div className="form-grid">
        <Field label="Issuer URL" id="oidc-issuer">
          <Input
            id="oidc-issuer"
            type="url"
            value={text(state.settings.issuerUrl)}
            onChange={(event) => state.set("issuerUrl", event.target.value)}
            placeholder="https://sso.example.com/realms/company"
          />
        </Field>
        <Field label="Client ID" id="oidc-client">
          <Input
            id="oidc-client"
            value={text(state.settings.clientId, "appstore")}
            onChange={(event) => state.set("clientId", event.target.value)}
          />
        </Field>
        <Field
          label="Client Secret"
          id="oidc-secret"
          help="기존 값은 조회되지 않습니다. 변경할 때만 입력하세요."
        >
          <Input
            id="oidc-secret"
            type="password"
            autoComplete="new-password"
            value={secret}
            onChange={(event) => setSecret(event.target.value)}
            placeholder="저장된 Secret은 표시하지 않음"
          />
        </Field>
        <Field
          label="Redirect URL"
          id="oidc-redirect"
          help="현재 서비스 URL에서 자동 생성됩니다."
        >
          <Input
            id="oidc-redirect"
            value={`${location.origin}/api/v1/auth/oidc/callback`}
            readOnly
          />
        </Field>
        <Field label="Role Claim Path" id="role-claim">
          <Input
            id="role-claim"
            value={text(state.settings.roleClaimPath)}
            onChange={(event) => state.set("roleClaimPath", event.target.value)}
          />
        </Field>
        <Field label="Group Claim Path" id="group-claim">
          <Input
            id="group-claim"
            value={text(state.settings.groupClaimPath)}
            onChange={(event) =>
              state.set("groupClaimPath", event.target.value)
            }
          />
        </Field>
      </div>
      <h2 className="section-title !text-xl !mt-4 !mb-4">Role Mapping</h2>
      <div className="form-grid">
        {ROLE_MAPPING_FIELDS.map(([internalRole, label]) => (
          <Field
            key={internalRole}
            label={label}
            id={`mapping-${internalRole}`}
          >
            <Input
              id={`mapping-${internalRole}`}
              value={externalRoleFor(state.settings.roleMappings, internalRole)}
              onChange={(event) =>
                state.set(
                  "roleMappings",
                  updateRoleMapping(
                    state.settings.roleMappings,
                    internalRole,
                    event.target.value,
                  ),
                )
              }
            />
          </Field>
        ))}
      </div>
      {test.data && (
        <div className="notice mt-5">
          <CheckCircle2 /> 연결 테스트가 정상 완료되었습니다.
        </div>
      )}
      {test.error && (
        <div className="notice notice-danger mt-5">{test.error.message}</div>
      )}
    </SettingsShell>
  );
}

export function AdminAiPage() {
  const state = useAdminSettings("ai", {
    name: "OpenAI Compatible",
    kind: "openai-compatible",
    baseUrl: "",
    defaultModel: "",
    contextWindow: 262144,
    maxInputTokens: 253952,
    maxOutputTokens: 8192,
    temperature: 0.7,
    timeoutSeconds: 120,
    retries: 1,
    streaming: true,
    enabled: true,
  });
  const [secret, setSecret] = useState("");
  const [prompt, setPrompt] = useState(
    "AppStore의 핵심 기능을 세 문장으로 설명해 줘.",
  );
  const [maxTokens, setMaxTokens] = useState(2048);
  const [selectedModelName, setSelectedModelName] = useState("");
  const [modelDraft, setModelDraft] = useState<AiModelLimit>({
    providerId: "",
    name: "",
    contextWindow: 262144,
    maxInputTokens: 253952,
    maxOutputTokens: 8192,
    enabled: true,
  });
  const [output, setOutput] = useState("");
  const [usage, setUsage] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [streamError, setStreamError] = useState("");
  const controller = useRef<AbortController | null>(null);
  const queryClient = useQueryClient();
  const providerId = text(state.settings.id);
  const models = useQuery({
    queryKey: ["admin", "ai", "models", providerId],
    queryFn: ({ signal }) => api.aiModels(providerId, signal),
    enabled: !!providerId,
  });
  const saveModel = useMutation({
    mutationFn: (input: AiModelLimit) => api.upsertAiModel(input),
    onSuccess: async () => {
      setModelDraft({
        providerId,
        name: "",
        contextWindow: 262144,
        maxInputTokens: 253952,
        maxOutputTokens: 8192,
        enabled: true,
      });
      await queryClient.invalidateQueries({
        queryKey: ["admin", "ai", "models", providerId],
      });
    },
  });
  const deleteModel = useMutation({
    mutationFn: (id: string) => api.deleteAiModel(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["admin", "ai", "models", providerId],
      });
    },
  });
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    setModelDraft((current) => ({ ...current, providerId }));
  }, [providerId]);
  const streamModelName =
    selectedModelName || text(state.settings.defaultModel);
  const selectedModel = models.data?.find(
    (model) => model.name === streamModelName,
  );
  const streamOutputLimit = Math.max(
    0,
    Math.min(
      262144,
      selectedModel?.maxOutputTokens ??
        number(state.settings.maxOutputTokens, 8192),
    ),
  );
  const providerLimitInvalid =
    number(state.settings.maxInputTokens, 253952) +
      number(state.settings.maxOutputTokens, 8192) >
    number(state.settings.contextWindow, 262144);
  const modelLimitInvalid =
    modelDraft.maxInputTokens + modelDraft.maxOutputTokens >
    modelDraft.contextWindow;
  const runStream = async () => {
    controller.current?.abort();
    controller.current = new AbortController();
    setStreaming(true);
    setOutput("");
    setUsage("");
    setStreamError("");
    try {
      await streamAiChat(
        {
          providerId: text(state.settings.id),
          messages: [{ role: "user", content: prompt }],
          model: streamModelName,
          maxTokens: Math.min(clampToken(maxTokens), streamOutputLimit),
        },
        (event) => {
          if (event.event === "message" || event.event === "token") {
            const data = event.data;
            const chunk =
              typeof data === "string"
                ? data
                : data && typeof data === "object"
                  ? text(
                      (data as SettingsRecord).text ??
                        (data as SettingsRecord).content ??
                        (data as SettingsRecord).delta ??
                        (data as SettingsRecord).token,
                    )
                  : "";
            setOutput((current) => current + chunk);
          } else if (event.event === "usage")
            setUsage(
              typeof event.data === "string"
                ? event.data
                : JSON.stringify(event.data),
            );
          else if (event.event === "error")
            setStreamError(
              typeof event.data === "string"
                ? event.data
                : JSON.stringify(event.data),
            );
        },
        controller.current.signal,
      );
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError"))
        setStreamError(
          error instanceof Error ? error.message : "스트리밍 오류",
        );
    } finally {
      setStreaming(false);
    }
  };
  return (
    <div className="page">
      <PageHeader
        eyebrow="AI platform"
        title="AI Providers"
        description="Provider limit과 model limit을 분리하고, 최대 262,144 token을 안전하게 설정합니다."
      />
      {state.query.isPending && <LoadingState />}
      {state.query.error && (
        <ErrorState
          error={state.query.error}
          retry={() => void state.query.refetch()}
        />
      )}
      {!!state.query.data && (
        <div className="detail-body !grid-cols-1 xl:!grid-cols-[minmax(0,1.2fr)_minmax(400px,.8fr)]">
          <Card className="form-section">
            <h2 className="section-title mb-6">Provider 설정</h2>
            <div className="form-grid">
              <Field label="Provider Name" id="ai-provider">
                <Input
                  id="ai-provider"
                  value={text(state.settings.name)}
                  onChange={(event) => state.set("name", event.target.value)}
                />
              </Field>
              <Field label="Provider 종류" id="ai-kind">
                <Select
                  id="ai-kind"
                  value={text(state.settings.kind, "openai-compatible")}
                  onChange={(event) => state.set("kind", event.target.value)}
                >
                  <option value="openai-compatible">OpenAI Compatible</option>
                  <option value="vllm">vLLM</option>
                  <option value="ollama">Ollama</option>
                  <option value="custom">Custom</option>
                </Select>
              </Field>
              <Field label="Base URL" id="ai-url">
                <Input
                  id="ai-url"
                  type="url"
                  value={text(state.settings.baseUrl)}
                  onChange={(event) => state.set("baseUrl", event.target.value)}
                />
              </Field>
              <Field
                label="API Key"
                id="ai-key"
                help="기존 값은 복원하거나 조회할 수 없습니다."
              >
                <Input
                  id="ai-key"
                  type="password"
                  autoComplete="new-password"
                  value={secret}
                  onChange={(event) => setSecret(event.target.value)}
                  placeholder="저장된 Secret은 표시하지 않음"
                />
              </Field>
              <Field label="기본 Model" id="ai-model">
                <Input
                  id="ai-model"
                  value={text(state.settings.defaultModel)}
                  onChange={(event) =>
                    state.set("defaultModel", event.target.value)
                  }
                />
              </Field>
              <TokenField
                id="context-window"
                label="Context Window"
                value={number(state.settings.contextWindow, 262144)}
                set={(value) => state.set("contextWindow", value)}
              />
              <TokenField
                id="max-input"
                label="Max Input Tokens"
                value={number(state.settings.maxInputTokens, 253952)}
                set={(value) => state.set("maxInputTokens", value)}
              />
              <TokenField
                id="max-output"
                label="Max Output Tokens"
                value={number(state.settings.maxOutputTokens, 8192)}
                set={(value) => state.set("maxOutputTokens", value)}
              />
              <Field label="Temperature" id="temperature">
                <Input
                  id="temperature"
                  type="number"
                  min="0"
                  max="2"
                  step="0.1"
                  value={number(state.settings.temperature, 0.7)}
                  onChange={(event) =>
                    state.set("temperature", Number(event.target.value))
                  }
                />
              </Field>
              <Field label="Timeout (초)" id="ai-timeout">
                <Input
                  id="ai-timeout"
                  type="number"
                  min="1"
                  value={number(state.settings.timeoutSeconds, 120)}
                  onChange={(event) =>
                    state.set("timeoutSeconds", Number(event.target.value))
                  }
                />
              </Field>
              <Field label="Retry" id="ai-retry">
                <Input
                  id="ai-retry"
                  type="number"
                  min="0"
                  max="10"
                  value={number(state.settings.retries, 1)}
                  onChange={(event) =>
                    state.set("retries", Number(event.target.value))
                  }
                />
              </Field>
            </div>
            <SettingSwitch
              state={state}
              name="streaming"
              label="Streaming 기본 사용"
            />
            <SettingSwitch
              state={state}
              name="enabled"
              label="Provider 활성화"
            />
            <div className="form-actions">
              <Button
                disabled={state.save.isPending || providerLimitInvalid}
                onClick={() =>
                  state.save.mutate(
                    secret
                      ? { ...state.settings, apiKey: secret }
                      : state.settings,
                  )
                }
              >
                <Save size={18} /> 설정 저장
              </Button>
            </div>
            {providerLimitInvalid && (
              <p className="field-error" role="alert">
                Max Input Tokens와 Max Output Tokens의 합은 Context Window를
                넘을 수 없습니다.
              </p>
            )}
            {state.save.error && (
              <p className="field-error">{state.save.error.message}</p>
            )}
          </Card>
          <Card className="form-section">
            <h2 className="section-title mb-6">Streaming 연결 테스트</h2>
            <Field
              label="Model"
              id="stream-model"
              help="선택한 모델의 Max Output Tokens를 테스트 상한으로 적용합니다."
            >
              <Select
                id="stream-model"
                value={streamModelName}
                onChange={(event) => {
                  const nextName = event.target.value;
                  const nextModel = models.data?.find(
                    (model) => model.name === nextName,
                  );
                  setSelectedModelName(nextName);
                  setMaxTokens((current) =>
                    Math.min(
                      current,
                      nextModel?.maxOutputTokens ??
                        number(state.settings.maxOutputTokens, 8192),
                    ),
                  );
                }}
              >
                {!models.data?.some(
                  (model) => model.name === text(state.settings.defaultModel),
                ) && (
                  <option value={text(state.settings.defaultModel)}>
                    {text(state.settings.defaultModel, "기본 모델")}
                  </option>
                )}
                {models.data
                  ?.filter((model) => model.enabled)
                  .map((model) => (
                    <option key={model.id ?? model.name} value={model.name}>
                      {model.name}
                    </option>
                  ))}
              </Select>
            </Field>
            <Field label="Prompt" id="stream-prompt">
              <Textarea
                id="stream-prompt"
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
              />
            </Field>
            <TokenField
              id="stream-max"
              label="Max Output Tokens"
              value={maxTokens}
              set={setMaxTokens}
              max={streamOutputLimit}
            />
            <div className="hero-actions mb-4">
              <Button
                disabled={streaming || !prompt.trim()}
                onClick={() => void runStream()}
              >
                <Play size={17} /> Streaming 시작
              </Button>
              <Button
                variant="danger"
                disabled={!streaming}
                onClick={() => controller.current?.abort()}
              >
                <Square size={16} /> 취소
              </Button>
            </div>
            <div
              className="stream-output"
              aria-live="polite"
              aria-busy={streaming}
            >
              {output ||
                (streaming
                  ? "응답을 기다리는 중…"
                  : "Streaming 결과가 여기에 표시됩니다.")}
            </div>
            <div className="stream-meta">
              <span>{streaming ? "연결됨 · 수신 중" : "대기"}</span>
              {usage && <span>Usage {usage}</span>}
              {streamError && (
                <span className="text-[var(--danger)]">{streamError}</span>
              )}
            </div>
          </Card>
        </div>
      )}
      {!!state.query.data && (
        <Card className="form-section mt-6">
          <div className="section-heading-row">
            <div>
              <h2 className="section-title mb-1">Model별 Token Limit</h2>
              <p className="muted">
                Provider 한도 안에서 각 model의 context, input, output 한도를
                독립적으로 관리합니다.
              </p>
            </div>
            <Badge>{models.data?.length ?? 0} models</Badge>
          </div>
          {!providerId && (
            <div className="notice mt-5">
              Provider를 먼저 저장한 뒤 model을 등록하세요.
            </div>
          )}
          {models.isPending && providerId && <LoadingState />}
          {models.error && (
            <ErrorState
              error={models.error}
              retry={() => void models.refetch()}
            />
          )}
          {!!models.data?.length && (
            <div className="settings-list mt-5">
              {models.data.map((model) => (
                <div className="settings-list-row" key={model.id ?? model.name}>
                  <div className="min-w-0">
                    <strong>{model.name}</strong>
                    <p className="muted">
                      Context {model.contextWindow.toLocaleString()} · Input{" "}
                      {model.maxInputTokens.toLocaleString()} · Output{" "}
                      {model.maxOutputTokens.toLocaleString()}
                    </p>
                  </div>
                  <Badge tone={model.enabled ? "positive" : undefined}>
                    {model.enabled ? "활성" : "비활성"}
                  </Badge>
                  <Button
                    variant="secondary"
                    onClick={() => setModelDraft({ ...model, providerId })}
                  >
                    편집
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={!model.id || deleteModel.isPending}
                    aria-label={`${model.name} 삭제`}
                    onClick={() => model.id && deleteModel.mutate(model.id)}
                  >
                    <Trash2 size={17} />
                  </Button>
                </div>
              ))}
            </div>
          )}
          {models.data?.length === 0 && providerId && (
            <EmptyState
              title="등록된 model이 없습니다"
              description="아래에서 첫 model의 token limit을 등록하세요."
            />
          )}
          <div className="form-grid mt-6">
            <Field label="Model Name" id="model-name">
              <Input
                id="model-name"
                value={modelDraft.name}
                onChange={(event) =>
                  setModelDraft((current) => ({
                    ...current,
                    name: event.target.value,
                  }))
                }
              />
            </Field>
            <TokenField
              id="model-context"
              label="Context Window"
              value={modelDraft.contextWindow}
              set={(value) =>
                setModelDraft((current) => ({
                  ...current,
                  contextWindow: value,
                }))
              }
            />
            <TokenField
              id="model-input"
              label="Max Input Tokens"
              value={modelDraft.maxInputTokens}
              set={(value) =>
                setModelDraft((current) => ({
                  ...current,
                  maxInputTokens: value,
                }))
              }
            />
            <TokenField
              id="model-output"
              label="Max Output Tokens"
              value={modelDraft.maxOutputTokens}
              set={(value) =>
                setModelDraft((current) => ({
                  ...current,
                  maxOutputTokens: value,
                }))
              }
            />
          </div>
          <div className="permission-option mt-4">
            <Switch
              checked={modelDraft.enabled}
              onChange={(checked) =>
                setModelDraft((current) => ({
                  ...current,
                  enabled: checked,
                }))
              }
              label="Model 활성화"
            />
            <span>Model 활성화</span>
          </div>
          <div className="form-actions">
            <Button
              disabled={
                !providerId ||
                !modelDraft.name.trim() ||
                saveModel.isPending ||
                modelLimitInvalid
              }
              onClick={() =>
                saveModel.mutate({
                  providerId,
                  name: modelDraft.name.trim(),
                  contextWindow: clampToken(modelDraft.contextWindow),
                  maxInputTokens: clampToken(modelDraft.maxInputTokens),
                  maxOutputTokens: clampToken(modelDraft.maxOutputTokens),
                  enabled: modelDraft.enabled,
                })
              }
            >
              <Save size={18} /> Model 저장
            </Button>
            {modelDraft.name && (
              <Button
                variant="secondary"
                onClick={() =>
                  setModelDraft({
                    providerId,
                    name: "",
                    contextWindow: 262144,
                    maxInputTokens: 253952,
                    maxOutputTokens: 8192,
                    enabled: true,
                  })
                }
              >
                새 Model
              </Button>
            )}
          </div>
          {modelLimitInvalid && (
            <p className="field-error" role="alert">
              Model의 input과 output 합은 context window를 넘을 수 없습니다.
            </p>
          )}
          {(saveModel.error || deleteModel.error) && (
            <p className="field-error">
              {(saveModel.error ?? deleteModel.error)?.message}
            </p>
          )}
        </Card>
      )}
    </div>
  );
}

function TokenField({
  id,
  label,
  value,
  set,
  max = 262144,
}: {
  id: string;
  label: string;
  value: number;
  set: (value: number) => void;
  max?: number;
}) {
  const boundedMax = Math.max(0, Math.min(262144, max));
  return (
    <Field label={label} id={id} help={`0 ~ ${boundedMax.toLocaleString()}`}>
      <Input
        id={id}
        type="number"
        min={0}
        max={boundedMax}
        step={1}
        value={value}
        onChange={(event) =>
          set(Math.min(boundedMax, clampToken(Number(event.target.value))))
        }
      />
    </Field>
  );
}

export function AdminApiPage() {
  const state = useAdminSettings("api", {
    enabled: true,
    anonymous: true,
    rateLimitPerMinute: 120,
  });
  return (
    <SettingsShell
      title="REST API"
      eyebrow="API platform"
      description="Versioned REST API와 익명 접근, 요청 제한을 관리합니다."
      state={state}
    >
      <SettingSwitch state={state} name="enabled" label="REST API 활성화" />
      <SettingSwitch
        state={state}
        name="anonymous"
        label="Anonymous API 허용"
      />
      <Field label="분당 요청 제한" id="api-rate">
        <Input
          id="api-rate"
          type="number"
          min={1}
          value={number(state.settings.rateLimitPerMinute, 120)}
          onChange={(event) =>
            state.set("rateLimitPerMinute", Number(event.target.value))
          }
        />
      </Field>
      <div className="notice">
        <Activity size={19} /> OpenAPI 문서: <code>/openapi.json</code> · API
        문서: <code>/docs</code>
      </div>
    </SettingsShell>
  );
}
export function AdminMcpPage() {
  const state = useAdminSettings("mcp", {
    enabled: true,
    anonymous: false,
    rateLimitPerMinute: 60,
    protocolVersion: "2026-07-28",
  });
  return (
    <SettingsShell
      title="MCP Server"
      eyebrow="Agent integration"
      description="MCP endpoint와 익명 tool 노출, 권한 및 rate limit을 관리합니다."
      state={state}
    >
      <SettingSwitch state={state} name="enabled" label="MCP 활성화" />
      <SettingSwitch
        state={state}
        name="anonymous"
        label="Anonymous MCP 접근"
      />
      <Field label="분당 요청 제한" id="mcp-rate">
        <Input
          id="mcp-rate"
          type="number"
          min={1}
          value={number(state.settings.rateLimitPerMinute, 60)}
          onChange={(event) =>
            state.set("rateLimitPerMinute", Number(event.target.value))
          }
        />
      </Field>
      <Field label="Protocol Version" id="mcp-protocol">
        <Input
          id="mcp-protocol"
          value={text(state.settings.protocolVersion, "2026-07-28")}
          onChange={(event) => state.set("protocolVersion", event.target.value)}
        />
      </Field>
      <div className="notice">
        MCP Endpoint <code>/mcp</code>
      </div>
    </SettingsShell>
  );
}
export function AdminSecurityPage() {
  const client = useQueryClient();
  const state = useAdminSettings("security", {
    maxKeys: 5,
    defaultExpiryDays: 90,
    rotationGraceDays: 7,
    expireUnused: false,
    unusedExpiryDays: 0,
    forceRotation: false,
    forceRotationDays: 90,
    permissions: [],
    templates: [],
  });
  const permissions = arrayFrom<KeyPermissionDefinition>(
    state.settings.permissions,
    [],
  );
  const templates = arrayFrom<KeyPermissionTemplate>(
    state.settings.templates,
    [],
  );
  const [permissionDraft, setPermissionDraft] =
    useState<KeyPermissionDefinition>();
  const [templateDraft, setTemplateDraft] = useState<
    KeyPermissionTemplate | Omit<KeyPermissionTemplate, "id">
  >();
  const invalidate = async () => {
    await client.invalidateQueries({ queryKey: ["admin", "security"] });
    await client.invalidateQueries({ queryKey: ["key-permission-options"] });
  };
  const savePermission = useMutation({
    mutationFn: (input: KeyPermissionDefinition) =>
      api.updateKeyPermission(input.key, input),
    onSuccess: async () => {
      setPermissionDraft(undefined);
      await invalidate();
    },
  });
  const saveTemplate = useMutation({
    mutationFn: (
      input: KeyPermissionTemplate | Omit<KeyPermissionTemplate, "id">,
    ) => {
      const payload = {
        name: input.name,
        description: input.description,
        permissions: input.permissions,
      };
      return "id" in input
        ? api.updateKeyTemplate(input.id, payload)
        : api.createKeyTemplate(payload);
    },
    onSuccess: async () => {
      setTemplateDraft(undefined);
      await invalidate();
    },
  });
  const deleteTemplate = useMutation({
    mutationFn: (id: string) => api.deleteKeyTemplate(id),
    onSuccess: invalidate,
  });
  return (
    <SettingsShell
      title="Security & Key Policy"
      eyebrow="Key management"
      description="개인별 키 만료, 회전 유예와 권한 template을 관리합니다."
      state={state}
      transform={(settings) => ({
        maxKeys: settings.maxKeys,
        defaultExpiryDays: settings.defaultExpiryDays,
        rotationGraceDays: settings.rotationGraceDays,
        expireUnused: settings.expireUnused,
        unusedExpiryDays: settings.unusedExpiryDays,
        forceRotation: settings.forceRotation,
        forceRotationDays: settings.forceRotationDays,
      })}
    >
      <div className="form-grid">
        <Field label="사용자별 최대 키" id="max-keys">
          <Input
            id="max-keys"
            type="number"
            min={1}
            value={number(state.settings.maxKeys, 5)}
            onChange={(event) =>
              state.set("maxKeys", Number(event.target.value))
            }
          />
        </Field>
        <Field label="기본 만료일" id="expiry-days">
          <Input
            id="expiry-days"
            type="number"
            min={1}
            value={number(state.settings.defaultExpiryDays, 90)}
            onChange={(event) =>
              state.set("defaultExpiryDays", Number(event.target.value))
            }
          />
        </Field>
        <Field label="Rotation Grace Period (일)" id="grace-days">
          <Input
            id="grace-days"
            type="number"
            min={0}
            value={number(state.settings.rotationGraceDays, 7)}
            onChange={(event) =>
              state.set("rotationGraceDays", Number(event.target.value))
            }
          />
        </Field>
        <Field label="미사용 자동 만료 (일, 0=끔)" id="idle-days">
          <Input
            id="idle-days"
            type="number"
            min={0}
            value={number(state.settings.unusedExpiryDays)}
            onChange={(event) =>
              state.set("unusedExpiryDays", Number(event.target.value))
            }
          />
        </Field>
      </div>
      <SettingSwitch
        state={state}
        name="expireUnused"
        label="미사용 Key 자동 만료"
      />
      <SettingSwitch
        state={state}
        name="forceRotation"
        label="Key Rotation 강제"
      />
      <Field label="강제 Rotation 주기 (일)" id="force-rotation-days">
        <Input
          id="force-rotation-days"
          type="number"
          min={1}
          value={number(state.settings.forceRotationDays, 90)}
          onChange={(event) =>
            state.set("forceRotationDays", Number(event.target.value))
          }
        />
      </Field>
      <section className="mt-8" aria-labelledby="key-permissions-title">
        <div className="section-header">
          <div>
            <h2 id="key-permissions-title" className="section-title !text-xl">
              Key Permission Definitions
            </h2>
            <p className="page-description">
              비활성 권한은 새 개인 키 발급 화면에서 노출되지 않습니다.
            </p>
          </div>
        </div>
        {!permissions.length ? (
          <EmptyState title="정의된 Key 권한이 없습니다" />
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>이름</th>
                  <th>설명</th>
                  <th>상태</th>
                  <th>작업</th>
                </tr>
              </thead>
              <tbody>
                {permissions.map((permission) => (
                  <tr key={permission.key}>
                    <td>
                      <code>{permission.key}</code>
                    </td>
                    <td>{permission.name}</td>
                    <td>{permission.description || "—"}</td>
                    <td>
                      <Badge tone={permission.active ? "positive" : "warning"}>
                        {permission.active ? "활성" : "비활성"}
                      </Badge>
                    </td>
                    <td>
                      <Button
                        size="sm"
                        variant="secondary"
                        onClick={() => setPermissionDraft(permission)}
                      >
                        편집
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      <section className="mt-8" aria-labelledby="key-templates-title">
        <div className="section-header">
          <div>
            <h2 id="key-templates-title" className="section-title !text-xl">
              Permission Templates
            </h2>
            <p className="page-description">
              사용자가 키 생성 시 선택할 최소 권한 묶음입니다.
            </p>
          </div>
          <Button
            size="sm"
            onClick={() =>
              setTemplateDraft({ name: "", description: "", permissions: [] })
            }
          >
            <Plus size={16} /> Template 추가
          </Button>
        </div>
        {!templates.length ? (
          <EmptyState title="등록된 Permission Template이 없습니다" />
        ) : (
          <div className="card-grid">
            {templates.map((template) => (
              <Card className="prose-card" key={template.id}>
                <h3>{template.name}</h3>
                <p>{template.description || "설명 없음"}</p>
                <div className="badge-row">
                  {template.permissions.map((permission) => (
                    <Badge key={permission}>{permission}</Badge>
                  ))}
                </div>
                <div className="form-actions">
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => setTemplateDraft(template)}
                  >
                    편집
                  </Button>
                  <Button
                    size="sm"
                    variant="danger"
                    disabled={deleteTemplate.isPending}
                    onClick={() => {
                      if (confirm(`${template.name} template을 삭제할까요?`))
                        deleteTemplate.mutate(template.id);
                    }}
                  >
                    <Trash2 size={15} /> 삭제
                  </Button>
                </div>
              </Card>
            ))}
          </div>
        )}
      </section>
      <Dialog
        open={!!permissionDraft}
        onClose={() => setPermissionDraft(undefined)}
        title="Key Permission 편집"
      >
        {permissionDraft && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              savePermission.mutate(permissionDraft);
            }}
          >
            <Field label="Permission Key" id="permission-key">
              <Input id="permission-key" value={permissionDraft.key} readOnly />
            </Field>
            <Field label="표시 이름" id="permission-name">
              <Input
                id="permission-name"
                value={permissionDraft.name}
                onChange={(event) =>
                  setPermissionDraft({
                    ...permissionDraft,
                    name: event.target.value,
                  })
                }
                required
              />
            </Field>
            <Field label="설명" id="permission-description">
              <Textarea
                id="permission-description"
                value={permissionDraft.description ?? ""}
                onChange={(event) =>
                  setPermissionDraft({
                    ...permissionDraft,
                    description: event.target.value,
                  })
                }
              />
            </Field>
            <div className="switch-row">
              <strong>활성</strong>
              <Switch
                checked={permissionDraft.active}
                onChange={(active) =>
                  setPermissionDraft({ ...permissionDraft, active })
                }
                label="Key Permission 활성"
              />
            </div>
            {savePermission.error && (
              <div className="notice notice-danger">
                {savePermission.error.message}
              </div>
            )}
            <div className="form-actions">
              <Button
                variant="secondary"
                onClick={() => setPermissionDraft(undefined)}
              >
                취소
              </Button>
              <Button type="submit" disabled={savePermission.isPending}>
                저장
              </Button>
            </div>
          </form>
        )}
      </Dialog>
      <Dialog
        open={!!templateDraft}
        onClose={() => setTemplateDraft(undefined)}
        title={
          templateDraft && "id" in templateDraft
            ? "Template 편집"
            : "Template 추가"
        }
      >
        {templateDraft && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              saveTemplate.mutate(templateDraft);
            }}
          >
            <Field label="이름" id="template-name">
              <Input
                id="template-name"
                value={templateDraft.name}
                onChange={(event) =>
                  setTemplateDraft({
                    ...templateDraft,
                    name: event.target.value,
                  })
                }
                required
              />
            </Field>
            <Field label="설명" id="template-description">
              <Textarea
                id="template-description"
                value={templateDraft.description ?? ""}
                onChange={(event) =>
                  setTemplateDraft({
                    ...templateDraft,
                    description: event.target.value,
                  })
                }
              />
            </Field>
            <fieldset className="field">
              <legend className="field-label">권한</legend>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                {permissions
                  .filter((permission) => permission.active)
                  .map((permission) => (
                    <label className="notice" key={permission.key}>
                      <input
                        type="checkbox"
                        checked={templateDraft.permissions.includes(
                          permission.key,
                        )}
                        onChange={(event) =>
                          setTemplateDraft({
                            ...templateDraft,
                            permissions: event.target.checked
                              ? [
                                  ...new Set([
                                    ...templateDraft.permissions,
                                    permission.key,
                                  ]),
                                ]
                              : templateDraft.permissions.filter(
                                  (key) => key !== permission.key,
                                ),
                          })
                        }
                      />{" "}
                      {permission.name}
                    </label>
                  ))}
              </div>
            </fieldset>
            {saveTemplate.error && (
              <div className="notice notice-danger">
                {saveTemplate.error.message}
              </div>
            )}
            <div className="form-actions">
              <Button
                variant="secondary"
                onClick={() => setTemplateDraft(undefined)}
              >
                취소
              </Button>
              <Button
                type="submit"
                disabled={
                  saveTemplate.isPending ||
                  !templateDraft.name.trim() ||
                  !templateDraft.permissions.length
                }
              >
                저장
              </Button>
            </div>
          </form>
        )}
      </Dialog>
    </SettingsShell>
  );
}
export function AdminSystemSettingsPage() {
  const state = useAdminSettings("settings", {
    siteName: "AppStore",
    siteUrl: location.origin,
    logoUrl: "",
    faviconUrl: "",
    defaultLanguage: "ko",
    pageSize: 24,
    publicMode: true,
    theme: "system",
  });
  return (
    <SettingsShell
      title="System Settings"
      eyebrow="Service configuration"
      description="서비스 이름과 접속 URL 등 운영 설정을 환경변수 변경 없이 관리합니다."
      state={state}
    >
      <div className="form-grid">
        <Field label="사이트 이름" id="site-name">
          <Input
            id="site-name"
            value={text(state.settings.siteName)}
            onChange={(event) => state.set("siteName", event.target.value)}
          />
        </Field>
        <Field
          label="서비스 접속 URL"
          id="site-url"
          help="사용자 화면에 노출되는 공식 접속 URL"
        >
          <Input
            id="site-url"
            type="url"
            value={text(state.settings.siteUrl)}
            onChange={(event) => state.set("siteUrl", event.target.value)}
          />
        </Field>
        <Field label="기본 언어" id="default-language">
          <Select
            id="default-language"
            value={text(state.settings.defaultLanguage, "ko")}
            onChange={(event) =>
              state.set("defaultLanguage", event.target.value)
            }
          >
            <option value="ko">한국어</option>
            <option value="en">English</option>
          </Select>
        </Field>
        <Field label="Logo URL" id="logo-url">
          <Input
            id="logo-url"
            type="url"
            value={text(state.settings.logoUrl)}
            onChange={(event) => state.set("logoUrl", event.target.value)}
            placeholder="https://appstore.example.internal/assets/logo.svg"
          />
        </Field>
        <Field label="Favicon URL" id="favicon-url">
          <Input
            id="favicon-url"
            type="url"
            value={text(state.settings.faviconUrl)}
            onChange={(event) => state.set("faviconUrl", event.target.value)}
          />
        </Field>
        <Field label="Page Size" id="page-size">
          <Input
            id="page-size"
            type="number"
            min={6}
            max={100}
            value={number(state.settings.pageSize, 24)}
            onChange={(event) =>
              state.set("pageSize", Number(event.target.value))
            }
          />
        </Field>
        <Field label="기본 Theme" id="theme">
          <Select
            id="theme"
            value={text(state.settings.theme, "system")}
            onChange={(event) => state.set("theme", event.target.value)}
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </Select>
        </Field>
      </div>
      <SettingSwitch
        state={state}
        name="publicMode"
        label="Public Mode"
        help="비로그인 사용자의 스토어 탐색을 허용합니다."
      />
    </SettingsShell>
  );
}

export function AdminApiKeysPage() {
  const keys = useQuery({
    queryKey: ["admin", "api-keys"],
    queryFn: ({ signal }) => api.admin<unknown>("api-keys", signal),
  });
  const rows = arrayFrom<PersonalKey & { ownerName?: string }>(keys.data, [
    "keys",
  ]);
  return (
    <div className="page">
      <PageHeader
        eyebrow="Key inventory"
        title="API Keys"
        description="원문 없이 prefix, 소유자, 권한과 사용 상태만 조회합니다."
      />
      {keys.isPending && <LoadingState />}
      {keys.error && (
        <ErrorState error={keys.error} retry={() => void keys.refetch()} />
      )}
      {!!keys.data && !rows.length && <EmptyState />}
      {!!rows.length && (
        <Card className="data-card">
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>소유자</th>
                  <th>이름</th>
                  <th>Prefix</th>
                  <th>권한</th>
                  <th>최근 사용</th>
                  <th>상태</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((key) => (
                  <tr key={key.id}>
                    <td>{key.ownerName || "—"}</td>
                    <td>{key.name}</td>
                    <td>
                      <code>{key.prefix}••••</code>
                    </td>
                    <td>{key.permissions.length}</td>
                    <td>{formatDateTime(key.lastUsedAt)}</td>
                    <td>
                      <Badge tone={key.revokedAt ? "danger" : "positive"}>
                        {key.revokedAt ? "폐기" : "활성"}
                      </Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  );
}

export function AdminAuditPage() {
  const [params, setParams] = useSearchParams();
  const action = params.get("action") ?? "";
  const audit = useQuery({
    queryKey: ["admin-audit", action],
    queryFn: ({ signal }) =>
      api.audit({ action, page: 1, pageSize: 200 }, signal),
  });
  return (
    <div className="page">
      <PageHeader
        eyebrow="Immutable history"
        title="Audit Logs"
        description="관리자를 포함한 주요 변경 행위가 삭제 불가능한 감사 기록으로 남습니다."
      />
      <div className="toolbar">
        <div className="field">
          <label htmlFor="audit-action">Action 필터</label>
          <Input
            id="audit-action"
            value={action}
            onChange={(event) =>
              setParams(
                event.target.value ? { action: event.target.value } : {},
              )
            }
            placeholder="Setting Change"
          />
        </div>
      </div>
      {audit.isPending && <LoadingState />}
      {audit.error && (
        <ErrorState error={audit.error} retry={() => void audit.refetch()} />
      )}
      {audit.data && !audit.data.items.length && <EmptyState />}
      {audit.data && !!audit.data.items.length && (
        <Card className="data-card">
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>시각</th>
                  <th>Actor</th>
                  <th>Action</th>
                  <th>Resource</th>
                  <th>IP</th>
                  <th>Request ID</th>
                </tr>
              </thead>
              <tbody>
                {audit.data.items.map((entry: AuditEntry) => (
                  <tr key={entry.id}>
                    <td>{formatDateTime(entry.createdAt)}</td>
                    <td>{entry.actor || "system"}</td>
                    <td>
                      <Badge>{entry.action}</Badge>
                    </td>
                    <td>{entry.resource || "—"}</td>
                    <td>{entry.ip || "—"}</td>
                    <td>
                      <code>{entry.requestId || "—"}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  );
}

export function AdminReviewsPage() {
  return <ReviewQueuePage />;
}

interface SettingsState {
  query: ReturnType<typeof useAdminSettings>["query"];
  settings: SettingsRecord;
  set: (key: string, value: unknown) => void;
  save: ReturnType<typeof useAdminSettings>["save"];
}
function SettingsShell({
  title,
  eyebrow,
  description,
  state,
  children,
  transform,
  extraActions,
}: {
  title: string;
  eyebrow: string;
  description: string;
  state: SettingsState;
  children: React.ReactNode;
  transform?: (settings: SettingsRecord) => SettingsRecord;
  extraActions?: React.ReactNode;
}) {
  return (
    <div className="page">
      <PageHeader eyebrow={eyebrow} title={title} description={description} />
      {state.query.isPending && <LoadingState />}
      {state.query.error && (
        <ErrorState
          error={state.query.error}
          retry={() => void state.query.refetch()}
        />
      )}
      {!!state.query.data && (
        <Card className="form-section">
          {children}
          {state.save.error && (
            <p className="field-error mt-5" role="alert">
              {state.save.error.message}
            </p>
          )}
          {state.save.isSuccess && (
            <div className="notice mt-5">
              <CheckCircle2 size={19} /> 설정이 저장되었습니다.
            </div>
          )}
          <div className="form-actions mt-6">
            {extraActions}
            <Button
              disabled={state.save.isPending}
              onClick={() =>
                state.save.mutate(
                  transform ? transform(state.settings) : state.settings,
                )
              }
            >
              <Save size={18} />{" "}
              {state.save.isPending ? "저장 중…" : "설정 저장"}
            </Button>
          </div>
        </Card>
      )}
    </div>
  );
}
function SettingSwitch({
  state,
  name,
  label,
  help,
}: {
  state: SettingsState;
  name: string;
  label: string;
  help?: string;
}) {
  return (
    <div className="switch-row">
      <div>
        <strong>{label}</strong>
        {help && <div className="field-help">{help}</div>}
      </div>
      <Switch
        checked={bool(state.settings[name])}
        onChange={(value) => state.set(name, value)}
        label={label}
      />
    </div>
  );
}
