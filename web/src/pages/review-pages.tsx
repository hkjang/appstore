import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ClipboardCheck, X } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { formatDateTime } from "../lib/utils";
import {
  Badge,
  Button,
  Card,
  Dialog,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Textarea,
} from "../components/ui";

export function ReviewQueuePage() {
  const reviews = useQuery({
    queryKey: ["reviews"],
    queryFn: ({ signal }) => api.reviews(signal),
  });
  return (
    <div className="page">
      <PageHeader
        eyebrow="Team review"
        title="검토 대기"
        description="Workflow가 활성화된 경우에만 등록 건이 이 목록에 나타납니다."
      />
      {reviews.isPending && <LoadingState />}
      {reviews.error && (
        <ErrorState
          error={reviews.error}
          retry={() => void reviews.refetch()}
        />
      )}
      {reviews.data && !reviews.data.length && (
        <EmptyState
          title="검토 대기 항목이 없습니다"
          description="모든 등록 요청을 처리했습니다."
        />
      )}
      {reviews.data && !!reviews.data.length && (
        <Card className="data-card">
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>앱</th>
                  <th>등록자</th>
                  <th>팀</th>
                  <th>상태</th>
                  <th>요청 시각</th>
                  <th>검토</th>
                </tr>
              </thead>
              <tbody>
                {reviews.data.map((review) => (
                  <tr key={review.id}>
                    <td>
                      <strong>
                        {review.appName || review.appSlug || review.appId}
                      </strong>
                    </td>
                    <td>{review.submitterName || "—"}</td>
                    <td>{review.team || "—"}</td>
                    <td>
                      <Badge
                        tone={
                          review.status === "pending"
                            ? "warning"
                            : review.status === "approved"
                              ? "positive"
                              : "danger"
                        }
                      >
                        {review.status}
                      </Badge>
                    </td>
                    <td>{formatDateTime(review.createdAt)}</td>
                    <td>
                      <Link
                        className="button button-secondary button-sm"
                        to={`/review/${review.id}`}
                      >
                        열기
                      </Link>
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

export function ReviewDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const review = useQuery({
    queryKey: ["review", id],
    queryFn: ({ signal }) => api.review(id, signal),
  });
  const [rejectOpen, setRejectOpen] = useState(false);
  const [reason, setReason] = useState("");
  const approve = useMutation({
    mutationFn: () => api.approveReview(id),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["reviews"] });
      navigate("/review");
    },
  });
  const reject = useMutation({
    mutationFn: () => api.rejectReview(id, reason),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["reviews"] });
      navigate("/review");
    },
  });
  if (review.isPending)
    return (
      <div className="page">
        <LoadingState />
      </div>
    );
  if (review.error)
    return (
      <div className="page">
        <ErrorState error={review.error} retry={() => void review.refetch()} />
      </div>
    );
  if (!review.data) return null;
  return (
    <div className="page">
      <PageHeader
        eyebrow="Review request"
        title={review.data.appName || review.data.appSlug || "앱 등록 검토"}
        description={`${review.data.submitterName || "등록자"} · ${formatDateTime(review.data.createdAt)}`}
        actions={<Badge tone="warning">{review.data.status}</Badge>}
      />
      <Card className="prose-card">
        <h2>검토 정보</h2>
        <dl className="meta-list">
          <div className="meta-row">
            <dt>앱 ID</dt>
            <dd>{review.data.appId}</dd>
          </div>
          <div className="meta-row">
            <dt>등록자</dt>
            <dd>{review.data.submitterName || "—"}</dd>
          </div>
          <div className="meta-row">
            <dt>담당팀</dt>
            <dd>{review.data.team || "—"}</dd>
          </div>
          <div className="meta-row">
            <dt>이전 사유</dt>
            <dd>{review.data.reason || "—"}</dd>
          </div>
        </dl>
        <div className="form-actions mt-6">
          <Button
            variant="danger"
            disabled={review.data.status !== "pending"}
            onClick={() => setRejectOpen(true)}
          >
            <X size={18} /> 반려
          </Button>
          <Button
            disabled={review.data.status !== "pending" || approve.isPending}
            onClick={() => {
              if (confirm("이 앱을 승인하고 게시할까요?")) approve.mutate();
            }}
          >
            <Check size={18} /> 승인 및 게시
          </Button>
        </div>
        {(approve.error || reject.error) && (
          <div className="notice notice-danger mt-5">
            {(approve.error || reject.error)?.message}
          </div>
        )}
      </Card>
      <Dialog
        open={rejectOpen}
        onClose={() => setRejectOpen(false)}
        title="앱 등록 반려"
        description="등록자가 수정할 수 있도록 구체적인 사유를 입력하세요."
      >
        <label className="field">
          <span className="field-label">반려 사유</span>
          <Textarea
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            required
          />
        </label>
        <div className="form-actions">
          <Button variant="secondary" onClick={() => setRejectOpen(false)}>
            취소
          </Button>
          <Button
            variant="danger"
            disabled={!reason.trim() || reject.isPending}
            onClick={() => reject.mutate()}
          >
            <ClipboardCheck size={18} /> 반려 확정
          </Button>
        </div>
      </Dialog>
    </div>
  );
}
