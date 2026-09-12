import { ExternalLink, Heart, ServerCog } from "lucide-react";
import { Link } from "react-router-dom";
import { appGlyph, formatDate } from "../../lib/utils";
import type { StoreApp } from "../../types";
import { AppIcon, Badge, Button } from "../../components/ui";
import { useFavorites } from "./favorites";
import { AppAdminLink, useCanManageApps } from "./admin-shortcut";
import { AppStatusBadge } from "./app-status";

// The public catalog only ever lists published apps, so the status is noise
// there; an owner's own list mixes drafts, pending, rejected and archived apps
// and needs it to tell them apart.
export function AppCard({
  app,
  showStatus = false,
}: {
  app: StoreApp;
  showStatus?: boolean;
}) {
  const { isFavorite, toggle } = useFavorites();
  const favorite = isFavorite(app.slug);
  const canManage = useCanManageApps();
  return (
    <article className="card app-card">
      <div className="app-card-body">
        <div className="app-card-head">
          <AppIcon app={app} />
          <div className="min-w-0">
            <h2 className="app-card-name">
              <Link to={`/apps/${encodeURIComponent(app.slug)}`}>
                {app.name}
              </Link>
            </h2>
            <p className="app-card-summary">{app.summary}</p>
          </div>
        </div>
        <div className="badge-row" aria-label="앱 특성">
          {showStatus && <AppStatusBadge status={app.status} />}
          {(app.category?.name || app.categoryName) && (
            <Badge>{app.category?.name || app.categoryName}</Badge>
          )}
          {app.language && <Badge>{app.language}</Badge>}
          {app.supportsMcp && (
            <Badge tone="primary">
              <ServerCog size={14} /> MCP
            </Badge>
          )}
          {app.supportsApi && <Badge tone="positive">API</Badge>}
        </div>
      </div>
      <footer className="app-card-footer">
        <span className="text-[13px] text-[var(--text-muted)]">
          {app.updatedAt
            ? `${formatDate(app.updatedAt)} 업데이트`
            : app.version
              ? `v${app.version.replace(/^v/, "")}`
              : "앱 정보 보기"}
        </span>
        <div className="top-actions">
          <Button
            variant="ghost"
            size="sm"
            aria-label={`${app.name} ${favorite ? "즐겨찾기 해제" : "즐겨찾기 추가"}`}
            aria-pressed={favorite}
            onClick={() => toggle(app.slug)}
          >
            <Heart size={17} fill={favorite ? "currentColor" : "none"} />
          </Button>
          {canManage && (
            <AppAdminLink appId={app.id} appName={app.name} compact />
          )}
          <Link
            className="button button-secondary button-sm"
            to={`/apps/${encodeURIComponent(app.slug)}`}
          >
            자세히 <ExternalLink size={15} aria-hidden="true" />
          </Link>
        </div>
      </footer>
    </article>
  );
}

export function AppAvatar({ name, icon }: { name: string; icon?: string }) {
  return (
    <span className="app-icon" aria-hidden="true">
      {appGlyph(name, icon)}
    </span>
  );
}
