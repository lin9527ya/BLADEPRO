"use client";

/**
 * SRC 风险面选择器。风险面短名必须与后端 srcmode.Areas 对齐；这里只用于
 * focus/exclude 的软偏好，必做面仍由 Program/任务快照决定，不能在前端放宽。
 */
export const SRC_AREA_OPTIONS: { name: string; label: string }[] = [
  { name: "rce", label: "RCE" },
  { name: "injection", label: "注入" },
  { name: "authz", label: "越权/IDOR" },
  { name: "upload", label: "上传" },
  { name: "session", label: "会话/登录" },
  { name: "oauth", label: "OAuth/OIDC" },
  { name: "graphql", label: "GraphQL" },
  { name: "websocket", label: "WebSocket" },
  { name: "supply", label: "供应链" },
  { name: "cache", label: "缓存" },
  { name: "desync", label: "走私/desync" },
  { name: "logic", label: "业务逻辑" },
  { name: "ssrf", label: "SSRF" },
  { name: "unauth", label: "未授权" },
  { name: "cred", label: "凭证泄露" },
  { name: "xss", label: "XSS" },
  { name: "takeover", label: "子域接管" },
  { name: "host", label: "主机暴露" },
  { name: "paths", label: "敏感路径" },
  { name: "csrf", label: "CSRF" },
];

export const SRC_AREA_NAMES = SRC_AREA_OPTIONS.map((a) => a.name);

/** 把逗号/空白分隔的文本归一成合法风险面短名。 */
export function parseAreaList(text: string): string[] {
  const known = new Set(SRC_AREA_NAMES);
  return [
    ...new Set(
      text
        .split(/[\s,，、]+/)
        .map((s) => s.trim())
        .filter((s) => known.has(s)),
    ),
  ];
}

/** 风险面短名 → 展示文本（未知值原样返回）。 */
export function srcAreaLabel(name: string): string {
  return SRC_AREA_OPTIONS.find((a) => a.name === name)?.label ?? name;
}

export function SrcAreaPicker({
  value,
  onChange,
  tone = "focus",
  disabled,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  tone?: "focus" | "exclude";
  disabled?: boolean;
}) {
  const selected = new Set(value);
  const active =
    tone === "focus"
      ? "border-sky-500/50 bg-sky-500/15 text-sky-700 dark:text-sky-300"
      : "border-amber-500/50 bg-amber-500/15 text-amber-700 dark:text-amber-300";
  return (
    <div className="flex flex-wrap gap-1">
      {SRC_AREA_OPTIONS.map((area) => {
        const on = selected.has(area.name);
        return (
          <button
            key={area.name}
            type="button"
            title={tone === "focus" ? `重点：${area.label}` : `排除：${area.label}（必做面仍会硬验收）`}
            aria-pressed={on}
            disabled={disabled}
            onClick={() => onChange(on ? value.filter((x) => x !== area.name) : [...value, area.name])}
            className={`rounded-full border px-2 py-0.5 text-xs transition-colors disabled:opacity-50 ${
              on ? active : "border-border text-muted-foreground hover:bg-muted"
            }`}
          >
            {area.label}
          </button>
        );
      })}
    </div>
  );
}
