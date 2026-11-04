"use client";

import * as React from "react";

import { SaveIcon, WrenchIcon } from "lucide-react";
import { toast } from "sonner";

import { Field, Section, SubBlock } from "@/components/page-sections";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";

interface Form {
  jadx_bin: string;
  jadx_out_dir: string;
  ghidra_home: string;
  dnslog_api: string;
  vision_base_url: string;
  vision_model: string;
  vision_api_key: string;
  feishu_app_id: string;
  feishu_chat_id: string;
  feishu_app_secret: string;
}

const EMPTY: Form = {
  jadx_bin: "",
  jadx_out_dir: "",
  ghidra_home: "",
  dnslog_api: "",
  vision_base_url: "",
  vision_model: "",
  vision_api_key: "",
  feishu_app_id: "",
  feishu_chat_id: "",
  feishu_app_secret: "",
};

/** 外部工具配置：jadx / ghidra / dnslog / 看图 / 飞书自建应用。密钥只写不读。 */
export default function IntegrationsPage() {
  const [form, setForm] = React.useState<Form>(EMPTY);
  const [keySet, setKeySet] = React.useState(false);
  const [secretSet, setSecretSet] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => {
    api
      .settings()
      .then((s) => {
        setForm({
          jadx_bin: s.jadx_bin ?? "",
          jadx_out_dir: s.jadx_out_dir ?? "",
          ghidra_home: s.ghidra_home ?? "",
          dnslog_api: s.dnslog_api ?? "",
          vision_base_url: s.vision_base_url ?? "",
          vision_model: s.vision_model ?? "",
          vision_api_key: "",
          feishu_app_id: s.feishu_app_id ?? "",
          feishu_chat_id: s.feishu_chat_id ?? "",
          feishu_app_secret: "",
        });
        setKeySet(!!s.vision_api_key_set);
        setSecretSet(!!s.feishu_app_secret_set);
      })
      .catch((e: Error) => toast.error(`读取配置失败：${e.message}`));
  }, []);

  async function save() {
    setSaving(true);
    try {
      const patch: Record<string, unknown> = {
        jadx_bin: form.jadx_bin.trim(),
        jadx_out_dir: form.jadx_out_dir.trim(),
        ghidra_home: form.ghidra_home.trim(),
        dnslog_api: form.dnslog_api.trim(),
        vision_base_url: form.vision_base_url.trim(),
        vision_model: form.vision_model.trim(),
        feishu_app_id: form.feishu_app_id.trim(),
        feishu_chat_id: form.feishu_chat_id.trim(),
      };
      // 密钥留空 = 不改。
      if (form.vision_api_key.trim() !== "") patch.vision_api_key = form.vision_api_key.trim();
      if (form.feishu_app_secret.trim() !== "") patch.feishu_app_secret = form.feishu_app_secret.trim();
      await api.setSettings(patch);
      toast.success("已保存；逆向 / 看图 / 飞书工具即时生效");
      setForm({ ...form, vision_api_key: "", feishu_app_secret: "" });
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-1 flex-col gap-6">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <WrenchIcon className="size-5" />
            外部工具
          </h1>
          <p className="text-muted-foreground mt-1 text-sm">
            jadx 反编译、pyghidra 二进制分析、dnslog
            带外检测、看图（vision）与飞书自建应用。未配置时对应工具会返回明确错误， 其余功能不受影响；密钥只写不读。
          </p>
        </div>
        <Button size="sm" onClick={save} disabled={saving}>
          <SaveIcon className="size-4" />
          保存
        </Button>
      </div>

      <Section title="逆向与带外">
        <SubBlock title="jadx / ghidra / dnslog">
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="jadx 可执行文件" hint="Windows 一般是 jadx.bat；未配置时 jadx_* 工具不可用。">
              <Input
                value={form.jadx_bin}
                placeholder="C:\tools\jadx\bin\jadx.bat"
                onChange={(e) => setForm({ ...form, jadx_bin: e.target.value })}
              />
            </Field>
            <Field label="jadx 产物目录" hint="反编译输出落在这里；建议放在 data/ 下。">
              <Input
                value={form.jadx_out_dir}
                placeholder="D:\Code\BLADEPRO\data\jadx"
                onChange={(e) => setForm({ ...form, jadx_out_dir: e.target.value })}
              />
            </Field>
            <Field label="Ghidra 安装目录（可选）" hint="配置后才能用 ghidra_analyze。">
              <Input
                value={form.ghidra_home}
                placeholder="C:\ghidra"
                onChange={(e) => setForm({ ...form, ghidra_home: e.target.value })}
              />
            </Field>
            <Field label="dnslog 服务地址" hint="带外验证（SSRF/RCE/XXE）用；需兼容 /api/session 与 /api/records。">
              <Input
                value={form.dnslog_api}
                placeholder="https://dnslog.example.com"
                onChange={(e) => setForm({ ...form, dnslog_api: e.target.value })}
              />
            </Field>
          </div>
        </SubBlock>
      </Section>

      <Section title="看图（vision）" hint="不填 Base URL / Key 时跟随全局激活的 LLM 配置；模型必须支持图片输入。">
        <SubBlock title="Anthropic 兼容端点">
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Base URL">
              <Input
                value={form.vision_base_url}
                placeholder="https://api.anthropic.com"
                onChange={(e) => setForm({ ...form, vision_base_url: e.target.value })}
              />
            </Field>
            <Field label="模型">
              <Input
                value={form.vision_model}
                placeholder="claude-sonnet-5-5"
                onChange={(e) => setForm({ ...form, vision_model: e.target.value })}
              />
            </Field>
            <Field label={`API Key${keySet ? "（已配置，留空不改）" : ""}`}>
              <Input
                type="password"
                autoComplete="new-password"
                value={form.vision_api_key}
                placeholder={keySet ? "已配置 · 留空表示不修改" : "留空则跟随全局激活 LLM"}
                onChange={(e) => setForm({ ...form, vision_api_key: e.target.value })}
              />
            </Field>
          </div>
        </SubBlock>
      </Section>

      <Section
        title="飞书自建应用"
        hint="用于 agent 工具发送文本/卡片/漏洞卡/图片/文件；与通知渠道里的 webhook 机器人并存。"
      >
        <SubBlock title="应用凭据">
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="App ID">
              <Input
                value={form.feishu_app_id}
                placeholder="cli_xxx"
                onChange={(e) => setForm({ ...form, feishu_app_id: e.target.value })}
              />
            </Field>
            <Field label="默认群 chat_id">
              <Input
                value={form.feishu_chat_id}
                placeholder="oc_xxx"
                onChange={(e) => setForm({ ...form, feishu_chat_id: e.target.value })}
              />
            </Field>
            <Field label={`App Secret${secretSet ? "（已配置，留空不改）" : ""}`}>
              <Input
                type="password"
                autoComplete="new-password"
                value={form.feishu_app_secret}
                placeholder={secretSet ? "已配置 · 留空表示不修改" : "飞书自建应用 Secret"}
                onChange={(e) => setForm({ ...form, feishu_app_secret: e.target.value })}
              />
            </Field>
          </div>
        </SubBlock>
      </Section>
    </div>
  );
}
