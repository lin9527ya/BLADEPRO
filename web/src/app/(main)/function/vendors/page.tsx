"use client";

import * as React from "react";

import { Building2Icon, RefreshCwIcon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Field, SubBlock } from "@/components/page-sections";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { CompanyProfile } from "@/lib/types";

function splitList(v: string): string[] {
  return v
    .split(/[\n,，]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/** 厂商档案：复用 companies，不新增 vendors 表（P-Q）。 */
export default function VendorsPage() {
  const [profiles, setProfiles] = React.useState<CompanyProfile[]>([]);
  const [selectedId, setSelectedId] = React.useState<string>("");
  const [form, setForm] = React.useState<CompanyProfile | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .companyProfiles()
      .then((r) => setProfiles(r.profiles))
      .catch((e: Error) => toast.error(`加载厂商档案失败：${e.message}`))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    load();
  }, [load]);

  React.useEffect(() => {
    if (!selectedId) {
      setForm(null);
      return;
    }
    const found = profiles.find((p) => p.company_id === Number(selectedId));
    if (found)
      setForm({ ...found, domains: found.domains ?? [], banned_vuln_classes: found.banned_vuln_classes ?? [] });
  }, [selectedId, profiles]);

  function patch<K extends keyof CompanyProfile>(key: K, value: CompanyProfile[K]) {
    setForm((f) => (f ? { ...f, [key]: value } : f));
  }

  async function save() {
    if (!form) return;
    setSaving(true);
    try {
      const saved = await api.saveCompanyProfile(form.company_id, {
        ...form,
        domains: form.domains ?? [],
        banned_vuln_classes: form.banned_vuln_classes ?? [],
      });
      toast.success("厂商档案已保存；下一次任务注入即生效");
      setForm(saved);
      load();
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-lg">
            <Building2Icon className="size-5" />
            厂商档案
          </h1>
          <p className="mt-1 text-muted-foreground text-sm">
            公司就是厂商：深度上限、归属域名、不收洞型、测试窗口与账号探活地址。任务按「显式企业 → 域名后缀 →
            任务文本」归因。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={load} disabled={loading}>
          <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
          刷新
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)] lg:items-start">
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>公司</TableHead>
                <TableHead className="w-16">深度</TableHead>
                <TableHead>归属域名</TableHead>
                <TableHead className="w-28">窗口</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {profiles.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground py-12 text-center text-sm">
                    {loading ? "加载中…" : "暂无公司：先在企业/资产页建公司，再在这里补厂商口径。"}
                  </TableCell>
                </TableRow>
              ) : (
                profiles.map((p) => (
                  <TableRow
                    key={p.company_id}
                    className="cursor-pointer"
                    data-state={String(p.company_id) === selectedId ? "selected" : undefined}
                    onClick={() => setSelectedId(String(p.company_id))}
                  >
                    <TableCell className="font-medium">{p.company_name}</TableCell>
                    <TableCell>
                      <Badge variant="outline">L{p.max_depth}</Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground line-clamp-1 text-xs">
                      {(p.domains ?? []).join(", ") || "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">{p.test_window || "—"}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </Card>

        <Card className="p-4">
          {!form ? (
            <div className="text-muted-foreground py-16 text-center text-sm">选择左侧公司开始编辑</div>
          ) : (
            <div className="flex flex-col gap-6">
              <div className="flex flex-wrap items-center gap-2 border-b pb-2">
                <h3 className="text-base font-medium">{form.company_name}</h3>
                <Badge variant="outline">L{form.max_depth}</Badge>
                <Button className="ml-auto" size="sm" onClick={save} disabled={saving}>
                  <SaveIcon className="size-4" />
                  保存
                </Button>
              </div>

              <SubBlock title="测试深度与窗口">
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="测试深度上限" hint="L1 存在证明 / L2 危害范围 / L3 控制力；任务级覆盖优先。">
                    <Select value={String(form.max_depth || 2)} onValueChange={(v) => patch("max_depth", Number(v))}>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {[1, 2, 3].map((d) => (
                          <SelectItem key={d} value={String(d)}>
                            L{d}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field label="测试窗口" hint="如 09:00-18:00；空 = 不限。">
                    <Input value={form.test_window ?? ""} onChange={(e) => patch("test_window", e.target.value)} />
                  </Field>
                  <Field label="频率说明" hint="文字口径，数值限速在平台项目里配置。">
                    <Input
                      value={form.rate_limit_note ?? ""}
                      onChange={(e) => patch("rate_limit_note", e.target.value)}
                    />
                  </Field>
                  <Field label="账号探活地址" hint="测试账号探活时 GET 这个地址。">
                    <Input value={form.check_url ?? ""} onChange={(e) => patch("check_url", e.target.value)} />
                  </Field>
                </div>
              </SubBlock>

              <SubBlock title="归属与红线">
                <Field label="归属域名" hint="一行或逗号分隔；用于把任务按域名后缀归因到这家公司。">
                  <Textarea
                    value={(form.domains ?? []).join("\n")}
                    placeholder={"example.com\nexample.cn"}
                    onChange={(e) => patch("domains", splitList(e.target.value))}
                  />
                </Field>
                <Field label="不收洞型" hint="逗号分隔，如 dos,self-xss；注入提示词后 agent 不会投入这些方向。">
                  <Input
                    value={(form.banned_vuln_classes ?? []).join(",")}
                    onChange={(e) => patch("banned_vuln_classes", splitList(e.target.value))}
                  />
                </Field>
              </SubBlock>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
