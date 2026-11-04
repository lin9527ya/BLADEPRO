"use client";

import * as React from "react";

import { CpuIcon, PencilIcon, PlusIcon, RadarIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Field, SubBlock } from "@/components/page-sections";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
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
import type { ADBDevice, Device } from "@/lib/types";

type DeviceForm = {
  id: number;
  name: string;
  kind: Device["kind"];
  conn: string;
  ssh_host: string;
  ssh_port: string;
  ssh_user: string;
  ssh_pass: string;
  ssh_key: string;
  tools_md: string;
};

const blankForm = (): DeviceForm => ({
  id: 0,
  name: "",
  kind: "adb",
  conn: "",
  ssh_host: "",
  ssh_port: "22",
  ssh_user: "",
  ssh_pass: "",
  ssh_key: "",
  tools_md: "",
});

function fmtTime(s?: string) {
  if (!s) return "—";
  return new Date(s).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

/** 设备注册表：adb/ssh/local；逆向与移动端测试从这里找真机。 */
export default function DevicesPage() {
  const [rows, setRows] = React.useState<Device[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [form, setForm] = React.useState<DeviceForm>(blankForm);
  const [sshTesting, setSshTesting] = React.useState(false);
  const [sshResult, setSshResult] = React.useState("");

  // ADB 扫描
  const [scanOpen, setScanOpen] = React.useState(false);
  const [scanning, setScanning] = React.useState(false);
  const [importing, setImporting] = React.useState(false);
  const [found, setFound] = React.useState<ADBDevice[]>([]);
  const [picked, setPicked] = React.useState<Record<string, boolean>>({});

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .devices()
      .then((r) => setRows(r.devices))
      .catch((e: Error) => toast.error(`加载设备失败：${e.message}`))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    load();
  }, [load]);

  const set = (patch: Partial<DeviceForm>) => setForm((f) => ({ ...f, ...patch }));

  function resetForm(kind?: Device["kind"]) {
    setForm({ ...blankForm(), ...(kind ? { kind } : {}) });
    setSshResult("");
  }

  function edit(d: Device) {
    setForm({
      id: d.id,
      name: d.name,
      kind: d.kind,
      conn: d.conn ?? "",
      ssh_host: d.ssh_host ?? "",
      ssh_port: String(d.ssh_port || 22),
      ssh_user: d.ssh_user ?? "",
      ssh_pass: "",
      ssh_key: d.ssh_key ?? "",
      tools_md: d.tools_md ?? "",
    });
    setSshResult("");
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  async function testSSH() {
    if (!form.ssh_host.trim() || !form.ssh_user.trim()) {
      toast.error("主机和用户名必填");
      return;
    }
    setSshTesting(true);
    setSshResult("");
    try {
      const r = await api.testSSHDevice({
        host: form.ssh_host.trim(),
        port: Number(form.ssh_port) || 22,
        user: form.ssh_user.trim(),
        pass: form.ssh_pass,
        key_path: form.ssh_key,
      });
      setSshResult(`${r.online ? "✅" : "❌"} ${r.note}`);
      toast[r.online ? "success" : "error"](r.note || (r.online ? "SSH 连接成功" : "SSH 连接失败"));
    } catch (e) {
      setSshResult(`❌ ${(e as Error).message}`);
      toast.error(`SSH 测试失败：${(e as Error).message}`);
    } finally {
      setSshTesting(false);
    }
  }

  async function save() {
    if (!form.name.trim()) {
      toast.error("名称必填");
      return;
    }
    if (form.kind === "ssh" && !form.ssh_host.trim()) {
      toast.error("SSH 设备需要填写主机");
      return;
    }
    setSaving(true);
    try {
      const d = await api.saveDevice({
        id: form.id || undefined,
        name: form.name.trim(),
        kind: form.kind,
        conn: form.conn.trim(),
        ssh_host: form.ssh_host.trim(),
        ssh_port: Number(form.ssh_port) || 22,
        ssh_user: form.ssh_user.trim(),
        ssh_pass: form.ssh_pass,
        ssh_key: form.ssh_key.trim(),
        tools_md: form.tools_md,
      });
      const action = form.id ? "已更新" : "已保存";
      try {
        const p = await api.probeDevice(d.id);
        toast[p.ok ? "success" : "error"](`${action}，探活：${p.note || (p.ok ? "在线" : "离线")}`);
      } catch {
        toast.success(action);
      }
      resetForm();
      load();
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  async function probe(d: Device) {
    try {
      const r = await api.probeDevice(d.id);
      toast[r.ok ? "success" : "error"](r.note || (r.ok ? "在线" : "离线"));
      load();
    } catch (e) {
      toast.error(`探活失败：${(e as Error).message}`);
    }
  }

  async function remove(d: Device) {
    if (!window.confirm(`删除设备「${d.name}」？`)) return;
    try {
      await api.deleteDevice(d.id);
      toast.success("已删除");
      if (form.id === d.id) resetForm();
      load();
    } catch (e) {
      toast.error(`删除失败：${(e as Error).message}`);
    }
  }

  // ── ADB 扫描 ──

  async function scan() {
    setScanning(true);
    setFound([]);
    setPicked({});
    try {
      const r = await api.scanADBDevices();
      setFound(r.devices);
      const init: Record<string, boolean> = {};
      for (const d of r.devices) init[d.serial] = !d.registered && d.state === "device";
      setPicked(init);
      if (r.devices.length === 0) toast.info("adb 没有发现已连接设备（检查 USB 调试 / adb 服务）");
    } catch (e) {
      toast.error(`扫描失败：${(e as Error).message}`);
    } finally {
      setScanning(false);
    }
  }

  function openScan() {
    setScanOpen(true);
    void scan();
  }

  async function importPicked() {
    const serials = Object.entries(picked)
      .filter(([, v]) => v)
      .map(([k]) => k);
    if (serials.length === 0) {
      toast.error("先勾选要登记的设备");
      return;
    }
    setImporting(true);
    try {
      const r = await api.importADBDevices(serials);
      const parts = [`新增 ${r.created.length}`];
      if (r.skipped.length) parts.push(`已存在 ${r.skipped.length}`);
      if (r.errors.length) parts.push(`失败 ${r.errors.length}`);
      toast.success(`已登记：${parts.join("，")}`);
      if (r.errors.length) toast.error(r.errors.join("；"));
      setScanOpen(false);
      load();
    } catch (e) {
      toast.error(`登记失败：${(e as Error).message}`);
    } finally {
      setImporting(false);
    }
  }

  const pickedCount = Object.values(picked).filter(Boolean).length;
  const unregistered = found.filter((d) => !d.registered && d.state === "device");

  return (
    <div className="flex flex-1 flex-col gap-6">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <CpuIcon className="size-5" />
            设备
          </h1>
          <p className="text-muted-foreground mt-1 text-sm">
            ADB / SSH / 本机设备登记与探活。逆向 agent 用 device_list / device_exec 在真机上执行只读命令；
            已连接的安卓设备可以直接扫描后一键登记。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={openScan}>
            <RadarIcon className="size-4" />
            扫描 ADB
          </Button>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
            刷新
          </Button>
        </div>
      </div>

      <Card className="p-4">
        <SubBlock
          title={form.id ? `编辑设备 #${form.id}` : "手动登记"}
          actions={
            <div className="flex items-center gap-2">
              {form.id > 0 && (
                <Button size="sm" variant="ghost" onClick={() => resetForm()}>
                  取消编辑
                </Button>
              )}
              <Button size="sm" onClick={save} disabled={saving}>
                <PlusIcon className="size-4" />
                {form.id ? "保存修改" : "保存设备"}
              </Button>
            </div>
          }
        >
          <div className="grid items-start gap-4 md:grid-cols-2 lg:grid-cols-4">
            <Field label="名称">
              <Input
                placeholder="如 Pixel 测试机 / VPS-hk"
                autoComplete="off"
                value={form.name}
                onChange={(e) => set({ name: e.target.value })}
              />
            </Field>
            <Field label="类型">
              <Select value={form.kind} onValueChange={(v) => set({ kind: v as Device["kind"] })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {["adb", "ssh", "local"].map((k) => (
                    <SelectItem key={k} value={k}>
                      {k}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>

            {form.kind === "adb" && (
              <Field label="adb 序列号" hint="不知道序列号就点右上角「扫描 ADB」自动发现。">
                <Input
                  placeholder="serial 或 ip:port"
                  autoComplete="off"
                  value={form.conn}
                  onChange={(e) => set({ conn: e.target.value })}
                />
              </Field>
            )}

            {form.kind === "ssh" && (
              <>
                <Field label="主机">
                  <Input
                    placeholder="192.168.1.100 或 example.com"
                    autoComplete="off"
                    value={form.ssh_host}
                    onChange={(e) => set({ ssh_host: e.target.value })}
                  />
                </Field>
                <Field label="端口">
                  <Input type="number" value={form.ssh_port} onChange={(e) => set({ ssh_port: e.target.value })} />
                </Field>
                <Field label="用户">
                  <Input
                    placeholder="root"
                    autoComplete="off"
                    value={form.ssh_user}
                    onChange={(e) => set({ ssh_user: e.target.value })}
                  />
                </Field>
                <Field label="密码" hint="密码或私钥至少填一个；凭据只写不读。">
                  <Input
                    type="password"
                    autoComplete="new-password"
                    value={form.ssh_pass}
                    onChange={(e) => set({ ssh_pass: e.target.value })}
                  />
                </Field>
                <Field label="私钥路径" hint="可选，与密码二选一。">
                  <Input
                    placeholder="C:\Users\me\.ssh\id_rsa"
                    autoComplete="off"
                    value={form.ssh_key}
                    onChange={(e) => set({ ssh_key: e.target.value })}
                  />
                </Field>
                <Field
                  label="连接测试"
                  hint={
                    sshResult ? (
                      <span className={sshResult.startsWith("✅") ? "text-green-600" : "text-destructive"}>
                        {sshResult}
                      </span>
                    ) : undefined
                  }
                >
                  <Button size="sm" variant="outline" className="w-full" onClick={testSSH} disabled={sshTesting}>
                    {sshTesting ? "测试中…" : "测试连接"}
                  </Button>
                </Field>
              </>
            )}
          </div>

          <Field
            label="设备能力说明（可选）"
            hint="设备上装了什么工具、App 列表、目录结构等；会作为上下文给逆向 agent。"
          >
            <Textarea
              rows={2}
              placeholder="例：已装 frida-server 16.1、objection；/sdcard/target.apk；可 USB 调试"
              value={form.tools_md}
              onChange={(e) => set({ tools_md: e.target.value })}
            />
          </Field>
        </SubBlock>
      </Card>

      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">#</TableHead>
              <TableHead>名称</TableHead>
              <TableHead className="w-20">类型</TableHead>
              <TableHead>地址</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-40">最近探活</TableHead>
              <TableHead className="w-52">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-muted-foreground py-12 text-center text-sm">
                  {loading ? "加载中…" : "暂无设备：点右上角「扫描 ADB」自动发现已连接的真机，或手动登记。"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((d) => (
                <TableRow key={d.id}>
                  <TableCell className="text-muted-foreground text-xs">{d.id}</TableCell>
                  <TableCell>
                    <div className="font-medium">{d.name}</div>
                    {d.tools_md && <div className="line-clamp-1 text-muted-foreground text-xs">{d.tools_md}</div>}
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">{d.kind}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono text-xs">
                    {d.kind === "ssh" ? `${d.ssh_user || "root"}@${d.ssh_host}:${d.ssh_port || 22}` : d.conn || "本机"}
                  </TableCell>
                  <TableCell>
                    <Badge variant={d.status === "online" ? "default" : "outline"}>{d.status}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {fmtTime(d.last_seen_at)}
                    {d.probe_note ? <div className="line-clamp-1">{d.probe_note}</div> : null}
                  </TableCell>
                  <TableCell>
                    <div className="flex gap-1.5">
                      <Button size="sm" variant="outline" onClick={() => probe(d)}>
                        探活
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => edit(d)}>
                        <PencilIcon className="size-3.5" />
                        编辑
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => remove(d)}>
                        删除
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={scanOpen} onOpenChange={setScanOpen}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <RadarIcon className="size-4" />
              扫描 ADB 设备
            </DialogTitle>
            <DialogDescription>
              运行 `adb devices -l`：已连接且状态为 device 的设备默认勾选；已登记过的会标出来，不会重复添加。
            </DialogDescription>
          </DialogHeader>

          <div className="flex items-center gap-2">
            <Button size="sm" variant="outline" onClick={scan} disabled={scanning}>
              <RefreshCwIcon className={scanning ? "size-4 animate-spin" : "size-4"} />
              重新扫描
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={scanning || unregistered.length === 0}
              onClick={() => {
                const next: Record<string, boolean> = { ...picked };
                for (const d of unregistered) next[d.serial] = true;
                setPicked(next);
              }}
            >
              全选未登记（{unregistered.length}）
            </Button>
          </div>

          <div className="max-h-[45vh] overflow-auto rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10" />
                  <TableHead>序列号</TableHead>
                  <TableHead className="w-28">型号</TableHead>
                  <TableHead className="w-24">状态</TableHead>
                  <TableHead className="w-24">登记</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {found.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={5} className="text-muted-foreground py-10 text-center text-sm">
                      {scanning ? "扫描中…" : "没有发现设备"}
                    </TableCell>
                  </TableRow>
                ) : (
                  found.map((d) => (
                    <TableRow key={d.serial}>
                      <TableCell>
                        <Checkbox
                          checked={!!picked[d.serial]}
                          disabled={d.registered}
                          onCheckedChange={(v) => setPicked({ ...picked, [d.serial]: !!v })}
                          aria-label={`选择 ${d.serial}`}
                        />
                      </TableCell>
                      <TableCell className="font-mono text-xs">{d.serial}</TableCell>
                      <TableCell className="text-xs">{d.model || d.product || "—"}</TableCell>
                      <TableCell>
                        <Badge variant={d.state === "device" ? "default" : "outline"}>{d.state}</Badge>
                      </TableCell>
                      <TableCell>
                        {d.registered ? (
                          <Badge variant="secondary">已登记</Badge>
                        ) : (
                          <span className="text-muted-foreground text-xs">未登记</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>

          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setScanOpen(false)}>
              取消
            </Button>
            <Button size="sm" onClick={importPicked} disabled={importing || pickedCount === 0}>
              {importing ? "登记中…" : `登记选中（${pickedCount}）`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
