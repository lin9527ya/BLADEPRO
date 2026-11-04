"use client";

import * as React from "react";

import { BookOpenIcon, Loader2Icon, RefreshCwIcon, SearchIcon, UploadIcon } from "lucide-react";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Field } from "@/components/page-sections";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { CompanyProfile, KnowledgeEntry } from "@/lib/types";

const TYPES = [
  { value: "all", label: "全部类型" },
  { value: "memory", label: "记忆" },
  { value: "ammo", label: "弹药" },
  { value: "playbook", label: "打法" },
  { value: "technique", label: "技术" },
  { value: "methodology", label: "方法论" },
  { value: "vendor_rule", label: "厂商规则" },
  { value: "fact", label: "事实" },
  { value: "conclusion", label: "结论" },
  { value: "asset_hint", label: "资产线索" },
];

const SCOPES = [
  { value: "all", label: "全部范围" },
  { value: "company", label: "公司" },
  { value: "global", label: "全局" },
  { value: "task", label: "任务" },
];

const STATUSES = [
  { value: "active", label: "生效中" },
  { value: "all", label: "全部状态" },
  { value: "stale", label: "待复核" },
  { value: "retired", label: "已退休" },
  { value: "deprecated", label: "已废弃" },
];

function fmtTime(s?: string) {
  if (!s) return "—";
  return new Date(s).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** 统一知识库：记忆 / 弹药 / 知识条目 / 打法合并为一个入口（P-Q）。 */
export default function KnowledgePage() {
  const [q, setQ] = React.useState("");
  const [qInput, setQInput] = React.useState("");
  const [scope, setScope] = React.useState("all");
  const [type, setType] = React.useState("all");
  const [status, setStatus] = React.useState("active");
  const [entries, setEntries] = React.useState<KnowledgeEntry[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [selected, setSelected] = React.useState<KnowledgeEntry | null>(null);
  const [note, setNote] = React.useState("");

  // 喂入（文档 / URL / 文本 → LLM 蒸馏 → 待审批提案）
  const [feedOpen, setFeedOpen] = React.useState(false);
  const [feedFile, setFeedFile] = React.useState<File | null>(null);
  const [feedURL, setFeedURL] = React.useState("");
  const [feedText, setFeedText] = React.useState("");
  const [feedCompany, setFeedCompany] = React.useState("0");
  const [feeding, setFeeding] = React.useState(false);
  const [profiles, setProfiles] = React.useState<CompanyProfile[]>([]);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .knowledge({
        q: q || undefined,
        scope: scope === "all" ? undefined : scope,
        types: type === "all" ? undefined : type,
        status: status === "all" ? undefined : status,
        limit: 100,
      })
      .then((r) => setEntries(r.entries))
      .catch((e: Error) => toast.error(`加载知识库失败：${e.message}`))
      .finally(() => setLoading(false));
  }, [q, scope, type, status]);

  React.useEffect(() => {
    load();
  }, [load]);

  React.useEffect(() => {
    api
      .companyProfiles()
      .then((r) => setProfiles(r.profiles))
      .catch(() => {});
  }, []);

  function resetFeed() {
    setFeedFile(null);
    setFeedURL("");
    setFeedText("");
    setFeedCompany("0");
  }

  async function submitFeed() {
    if (!feedFile && !feedURL.trim() && !feedText.trim()) {
      toast.error("请选择文件、填写 URL 或粘贴文本");
      return;
    }
    const companyId = Number(feedCompany) > 0 ? Number(feedCompany) : undefined;
    setFeeding(true);
    try {
      const r = feedFile
        ? await api.ingestKnowledgeFile(feedFile, { company_id: companyId })
        : await api.ingestKnowledgeText({
            url: feedURL.trim() || undefined,
            text: feedURL.trim() ? undefined : feedText,
            company_id: companyId,
          });
      toast.success(r.note || `已生成待审批提案 #${r.approval_id}`);
      if (r.distill_error) toast.error(`LLM 蒸馏失败，已按原文入库：${r.distill_error}`);
      setFeedOpen(false);
      resetFeed();
    } catch (e) {
      toast.error(`喂入失败：${(e as Error).message}`);
    } finally {
      setFeeding(false);
    }
  }

  async function feedback(e: KnowledgeEntry, outcome: "worked" | "failed" | "verified" | "stale") {
    try {
      await api.knowledgeFeedback(e.id, outcome, outcome === "stale" ? note : "");
      toast.success(
        outcome === "worked"
          ? "已记录：有效"
          : outcome === "failed"
            ? "已记录：无效"
            : outcome === "stale"
              ? "已标记待复核"
              : "已复核通过",
      );
      setNote("");
      setSelected(null);
      load();
    } catch (err) {
      toast.error(`操作失败：${(err as Error).message}`);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-lg">
            <BookOpenIcon className="size-5" />
            知识库
          </h1>
          <p className="mt-1 text-muted-foreground text-sm">
            跨任务记忆、弹药、知识条目与打法卡：批准后成为下一次任务的先验；注入块按热度与复核时间排序，长期未复核会标
            ⚠。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setFeedOpen(true)}>
            <UploadIcon className="size-4" />
            喂入
          </Button>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
            刷新
          </Button>
        </div>
      </div>

      <Card className="flex flex-row flex-wrap items-center gap-2 p-3">
        <div className="flex min-w-64 flex-1 items-center gap-2">
          <SearchIcon className="text-muted-foreground size-4 shrink-0" />
          <Input
            className="h-8 max-w-sm"
            placeholder="搜索 key / 标题 / 摘要 / 正文"
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") setQ(qInput.trim());
            }}
          />
          <Button size="sm" variant="secondary" onClick={() => setQ(qInput.trim())}>
            搜索
          </Button>
        </div>
        <Select value={scope} onValueChange={setScope}>
          <SelectTrigger size="sm" className="w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SCOPES.map((s) => (
              <SelectItem key={s.value} value={s.value}>
                {s.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={type} onValueChange={setType}>
          <SelectTrigger size="sm" className="w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TYPES.map((s) => (
              <SelectItem key={s.value} value={s.value}>
                {s.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={status} onValueChange={setStatus}>
          <SelectTrigger size="sm" className="w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {STATUSES.map((s) => (
              <SelectItem key={s.value} value={s.value}>
                {s.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Card>

      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">#</TableHead>
              <TableHead className="w-24">类型</TableHead>
              <TableHead className="w-20">范围</TableHead>
              <TableHead>标题 / 摘要</TableHead>
              <TableHead className="w-20">置信度</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-16 text-right">命中</TableHead>
              <TableHead className="w-20">实战</TableHead>
              <TableHead className="w-36">最近复核</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9} className="text-muted-foreground py-12 text-center text-sm">
                  {loading
                    ? "加载中…"
                    : "暂无知识条目：任务里用 record_knowledge / propose_knowledge 沉淀，批准后出现在这里。"}
                </TableCell>
              </TableRow>
            ) : (
              entries.map((e) => (
                <TableRow key={e.id} className="cursor-pointer" onClick={() => setSelected(e)}>
                  <TableCell className="text-muted-foreground text-xs">{e.id}</TableCell>
                  <TableCell>
                    <Badge variant="secondary">{e.entry_type}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{e.scope}</TableCell>
                  <TableCell>
                    <div className="line-clamp-1 font-medium">{e.title || e.key || e.summary}</div>
                    <div className="line-clamp-1 text-muted-foreground text-xs">{e.summary}</div>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{e.confidence || "—"}</TableCell>
                  <TableCell>
                    <Badge variant={e.status === "active" ? "default" : "outline"}>{e.status}</Badge>
                  </TableCell>
                  <TableCell className="text-right text-sm tabular-nums">{e.hits}</TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {e.successes + e.failures === 0 ? "—" : `${e.successes}/${e.successes + e.failures}`}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{fmtTime(e.last_verified_at)}</TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={!!selected} onOpenChange={(open) => !open && setSelected(null)}>
        <DialogContent className="max-h-[85vh] overflow-hidden sm:max-w-3xl">
          {selected && (
            <>
              <DialogHeader>
                <DialogTitle className="flex flex-wrap items-center gap-2">
                  <Badge variant="secondary">{selected.entry_type}</Badge>
                  <span className="line-clamp-1">{selected.title || selected.key || `#${selected.id}`}</span>
                </DialogTitle>
                <DialogDescription>
                  {selected.scope} · {selected.status} · 命中 {selected.hits} · 最近复核{" "}
                  {fmtTime(selected.last_verified_at)}
                </DialogDescription>
              </DialogHeader>
              <div className="max-h-[55vh] space-y-4 overflow-auto pr-1">
                <div>
                  <div className="mb-1 text-muted-foreground text-xs">摘要</div>
                  <p className="text-sm leading-relaxed">{selected.summary}</p>
                </div>
                {selected.body_md && (
                  <div>
                    <div className="mb-1 text-muted-foreground text-xs">正文</div>
                    <div className="rounded-md border p-3">
                      <Markdown text={selected.body_md} />
                    </div>
                  </div>
                )}
                {selected.evidence_ref && (
                  <div className="text-muted-foreground text-xs">证据：{selected.evidence_ref}</div>
                )}
                <Field
                  label="复核备注（标为待复核时必填）"
                  hint="写下为什么仍然有效 / 为什么失效，后来人最需要的就是这句。"
                >
                  <Textarea
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                    placeholder="例：目标已升级到新版，该 payload 不再生效"
                  />
                </Field>
              </div>
              <DialogFooter className="flex-wrap gap-2">
                <Button size="sm" variant="outline" onClick={() => feedback(selected, "worked")}>
                  有效
                </Button>
                <Button size="sm" variant="outline" onClick={() => feedback(selected, "failed")}>
                  无效
                </Button>
                <Button size="sm" variant="outline" onClick={() => feedback(selected, "stale")} disabled={!note.trim()}>
                  标为待复核
                </Button>
                <Button size="sm" onClick={() => feedback(selected, "verified")}>
                  复核通过
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={feedOpen} onOpenChange={(open) => !open && !feeding && setFeedOpen(false)}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <UploadIcon className="size-4" />
              喂入知识库
            </DialogTitle>
            <DialogDescription>
              支持 docx / pdf / md / txt 文档、网页 URL 或直接粘贴文本。有可用 LLM
              时会蒸馏成结构化草案，批准后写入知识库；扫描件请先 OCR。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <Field label="文档（可选）" hint="三选一：文件 > URL > 文本。文件上限 32MB。">
              <Input
                type="file"
                accept=".docx,.pdf,.md,.txt,.json,.csv,.html"
                disabled={feeding}
                onChange={(e) => setFeedFile(e.target.files?.[0] ?? null)}
              />
            </Field>
            <Field label="网页 URL（可选）" hint="只抓取 http/https 文本页面；内网/本机地址默认拒绝。">
              <Input
                placeholder="https://example.com/blog/post"
                value={feedURL}
                disabled={feeding || !!feedFile}
                onChange={(e) => setFeedURL(e.target.value)}
              />
            </Field>
            <Field label="文本（可选）" hint="直接粘贴笔记、聊天记录或文章正文；少于 100 字会被拒绝。">
              <Textarea
                rows={6}
                value={feedText}
                disabled={feeding || !!feedFile || !!feedURL.trim()}
                onChange={(e) => setFeedText(e.target.value)}
                placeholder="把要沉淀的知识贴在这里…"
              />
            </Field>
            <Field label="归属公司" hint="留空为全局知识；选择公司后仅在该公司任务中注入。">
              <Select value={feedCompany} onValueChange={setFeedCompany}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">全局</SelectItem>
                  {profiles.map((p) => (
                    <SelectItem key={p.company_id} value={String(p.company_id)}>
                      {p.company_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setFeedOpen(false)} disabled={feeding}>
              取消
            </Button>
            <Button size="sm" onClick={submitFeed} disabled={feeding}>
              {feeding ? <Loader2Icon className="size-4 animate-spin" /> : <UploadIcon className="size-4" />}
              {feeding ? "蒸馏中…（最长 2 分钟）" : "开始蒸馏"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
