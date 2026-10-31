// SRC 台账状态合同：两个 SRC 页面（项目页/任务页）共用同一份标签与合法流转。
// 服务端仍是最终判定；这里只用于少给错选项，避免两处各写一份后对不上。

export const SRC_SUBMISSION_STATUS_LABELS: Record<string, string> = {
  draft: "草稿",
  ready: "可提交",
  submitted: "已提交",
  accepted: "已受理",
  duplicate: "重复",
  rejected: "已驳回",
  paid: "已到账",
  withdrawn: "已撤回",
};

export const SRC_SUBMISSION_STATUS_ORDER = [
  "draft",
  "ready",
  "submitted",
  "accepted",
  "duplicate",
  "rejected",
  "paid",
  "withdrawn",
];

export const SRC_SUBMISSION_NEXT: Record<string, string[]> = {
  draft: ["ready", "withdrawn"],
  ready: ["submitted", "draft", "withdrawn"],
  submitted: ["accepted", "duplicate", "rejected", "withdrawn"],
  accepted: ["paid", "withdrawn"],
  duplicate: ["withdrawn"],
  rejected: ["withdrawn"],
  paid: [],
  withdrawn: [],
};

export const SRC_VERDICT_REASON_LABELS: Record<string, string> = {
  not_reproducible: "无法复现",
  duplicate: "已重复",
  insufficient_info: "信息不全",
  not_vulnerability: "非漏洞/不接收",
  out_of_scope: "超出范围",
  other: "其他",
};

export function srcSubmissionTone(status: string): string {
  switch (status) {
    case "ready":
      return "border-sky-500/40 bg-sky-500/15 text-sky-600 dark:text-sky-400";
    case "submitted":
      return "border-amber-500/40 bg-amber-500/15 text-amber-600 dark:text-amber-400";
    case "accepted":
      return "border-emerald-500/40 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400";
    case "paid":
      return "border-emerald-600/50 bg-emerald-600/20 text-emerald-700 dark:text-emerald-300";
    case "duplicate":
    case "rejected":
      return "border-border bg-muted text-muted-foreground";
    case "withdrawn":
      return "border-dashed border-border text-muted-foreground";
    default:
      return "border-border text-muted-foreground";
  }
}
