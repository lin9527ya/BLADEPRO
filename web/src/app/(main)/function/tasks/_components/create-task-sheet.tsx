"use client";

import * as React from "react";

import { ChevronRightIcon, Loader2Icon, PaperclipIcon, PlusIcon } from "lucide-react";
import { toast } from "sonner";

import { AssetInterceptRulesEditor } from "@/components/asset-intercept-rules-editor";
import { CustomRulesEditor } from "@/components/custom-rules-editor";
import { StatusBadge } from "@/components/status-badge";
import { TaskLLMProfileChain } from "@/components/task-llm-profile-chain";
import { TaskTemplateControls } from "@/components/task-template-controls";
import { AlertDialog } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
} from "@/components/ui/combobox";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  AssetInterceptRuleInput,
  Company,
  CustomRule,
  ExplorationPolicyDefault,
  LLMProfile,
  SrcProgram,
  Task,
  TaskCategory,
  TaskMode,
} from "@/lib/types";

import { CategoryPicker, CompanyPicker } from "./task-categories";
import { appendUploads, MAX_SOURCE_TASKS } from "./task-helpers";

// CreateTaskSheet is the 新建任务 drawer. Its form state lives HERE, not in TasksPage: with
// 描述/目标 held by the page component every keystroke re-rendered the whole task table
// behind the drawer (plus its sticky column and 20 AlertDialog trees), which showed up as
// input lag. Now typing only re-renders the drawer.
function SourceTaskPicker({
  tasks,
  value,
  onValueChange,
  portalContainer,
}: {
  tasks: Task[];
  value: string[];
  onValueChange: (value: string[]) => void;
  portalContainer?: React.RefObject<HTMLElement | null>;
}) {
  const tasksByID = React.useMemo(() => new Map(tasks.map((task) => [task.id, task])), [tasks]);
  const taskIDs = React.useMemo(() => tasks.map((task) => task.id), [tasks]);
  const atLimit = value.length >= MAX_SOURCE_TASKS;

  const handleValueChange = (next: string[]) => {
    onValueChange(next.slice(0, MAX_SOURCE_TASKS));
  };

  return (
    <Combobox
      items={taskIDs}
      itemToStringValue={(taskID) => {
        const task = tasksByID.get(taskID);
        return task ? `${task.id} ${task.description} ${task.goal}` : taskID;
      }}
      multiple
      value={value}
      onValueChange={handleValueChange}
    >
      <ComboboxChips>
        <ComboboxValue>
          {value.map((taskID) => (
            <ComboboxChip key={taskID}>#{taskID}</ComboboxChip>
          ))}
        </ComboboxValue>
        <ComboboxChipsInput
          id="source-tasks"
          placeholder={atLimit ? `最多关联 ${MAX_SOURCE_TASKS} 个任务` : "搜索任务 ID、描述或目标"}
          disabled={atLimit}
        />
      </ComboboxChips>
      <ComboboxContent portalContainer={portalContainer}>
        <ComboboxEmpty>没有匹配的任务</ComboboxEmpty>
        <ComboboxList>
          {(taskID) => {
            const task = tasksByID.get(taskID);
            return (
              <ComboboxItem key={taskID} value={taskID} disabled={atLimit && !value.includes(taskID)}>
                <div className="flex min-w-0 flex-1 items-center gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">
                      #{taskID} · {task?.description ?? "未知任务"}
                    </p>
                    {task?.goal && <p className="text-muted-foreground truncate text-xs">{task.goal}</p>}
                  </div>
                  {task && <StatusBadge domain="task" value={task.status} />}
                </div>
              </ComboboxItem>
            );
          }}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  );
}

// CategoryPicker 是新建任务里的单选分类选择器：可搜索已有分类；输入库里没有的名称后
// 回车（或点下拉里的「创建」）即时新建分类并选中，选中项以可移除的 tag 展示。分类是
// 全局资源，这里即时创建与「分类管理」里手动新建等价。只允许一个分类。

export function CreateTaskSheet({
  tasks,
  categories,
  onCreated,
  onCategoriesChanged,
}: {
  tasks: Task[];
  categories: TaskCategory[];
  onCreated: () => void;
  onCategoriesChanged: () => void;
}) {
  const [open, setOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [categoryID, setCategoryID] = React.useState<number | undefined>(undefined);
  // 工作台以 SRC 为主：默认 SRC；普通渗透仍可显式切换。
  const [mode, setMode] = React.useState<TaskMode>("src");
  // P3 平台项目:仅 SRC 模式下可选,绑定的项目规则(禁用手段/写操作/限速/必做面)编译进本任务。
  const [srcPrograms, setSrcPrograms] = React.useState<SrcProgram[]>([]);
  const [srcProgramID, setSrcProgramID] = React.useState<number | undefined>(undefined);
  // P2 跨任务覆盖继承：把近期同项目/同企业的 tested_clear 与收尾数字带进新任务。
  const [inheritCoverage, setInheritCoverage] = React.useState(false);
  // P2 资产级 token 预算（0 = 跟随系统全局默认）。
  const [assetTokenBudget, setAssetTokenBudget] = React.useState("0");
  const [assetTimeBudget, setAssetTimeBudget] = React.useState("0");
  const [assetCostBudget, setAssetCostBudget] = React.useState("0");
  // 提交块模板（空 = 按平台名自动识别；项目规则可覆盖）。
  const [platformTemplate, setPlatformTemplate] = React.useState("");
  // P2 批量扇出：按 host / 根域 / 数量分片，每片建一个任务并归入同一 campaign。
  const [batchFanout, setBatchFanout] = React.useState(false);
  const [shardBy, setShardBy] = React.useState<"host" | "root_domain">("host");
  const [assetsPerTask, setAssetsPerTask] = React.useState("20");
  const [maxShards, setMaxShards] = React.useState("50");
  const [batchTokenBudget, setBatchTokenBudget] = React.useState("0");
  const [campaignTokenBudget, setCampaignTokenBudget] = React.useState("0");
  const [campaignName, setCampaignName] = React.useState("");
  // 批量扇出的资产过滤：all=全部 / new=未被 SRC 任务关联过 / untested=没有任何终态格。
  const [batchAssetFilter, setBatchAssetFilter] = React.useState<"all" | "new" | "untested">("all");
  // 排队优先级：越大越先拿到并发槽；不抢占运行中任务。
  const [queuePriority, setQueuePriority] = React.useState("0");
  const [description, setDescription] = React.useState("");
  const [goal, setGoal] = React.useState("");
  const [selectedTemplateID, setSelectedTemplateID] = React.useState<number | null>(null);
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [companies, setCompanies] = React.useState<Company[]>([]);
  const [sourceTaskIDs, setSourceTaskIDs] = React.useState<string[]>([]);
  const [companyIDs, setCompanyIDs] = React.useState<number[]>([]);
  const [llmProfileIDs, setLLMProfileIDs] = React.useState<string[]>([]);
  const [creating, setCreating] = React.useState(false);
  const [timeoutMin, setTimeoutMin] = React.useState(""); // 任务级超时(分钟);空/0 = 不限时
  const [heartbeatMin, setHeartbeatMin] = React.useState("10"); // planner 心跳(分钟);默认10,下限10(与后端一致)
  const [seedFirstIntent, setSeedFirstIntent] = React.useState(false); // 创建时下发种子意图,worker 免等首轮 planner 直接开跑;默认关闭,走标准先规划再执行
  const [coverageEnabled, setCoverageEnabled] = React.useState(true); // 资产覆盖度功能;默认开。关闭=不计算/展示覆盖度、不累积范围、隐藏范围类工具(company 关联不受影响)
  const [interceptRules, setInterceptRules] = React.useState<AssetInterceptRuleInput[]>([]); // 任务级资产拦截规则(仅本任务生效,不进全局表)
  // P20 探索策略:默认继承当时的全局默认(服务端解析成快照);勾选"自定义"才发覆盖层。
  const [policyCustom, setPolicyCustom] = React.useState(false);
  const [policyVocab, setPolicyVocab] = React.useState<ExplorationPolicyDefault | null>(null);
  const [policyTech, setPolicyTech] = React.useState<string[]>([]);
  const [policyWrites, setPolicyWrites] = React.useState<string[]>([]);
  const [policyRules, setPolicyRules] = React.useState<CustomRule[]>([]);
  // 方式1 文件上传:建任务前把文件暂存到 drafts/<draftId>/uploads/,拿回绝对路径追加进描述。
  const [uploading, setUploading] = React.useState(false);
  const [uploadCount, setUploadCount] = React.useState(0);
  const draftIdRef = React.useRef<string>("");
  const fileInputRef = React.useRef<HTMLInputElement>(null);
  const sheetContentRef = React.useRef<HTMLDivElement>(null);

  // load LLM profiles once for the create-task profile picker.
  React.useEffect(() => {
    api
      .llmProfiles()
      .then(setProfiles)
      .catch(() => setProfiles([]));
    api
      .companies()
      .then(setCompanies)
      .catch(() => setCompanies([]));
  }, []);

  // pickFiles uploads the chosen files into this draft's staging dir and appends their
  // absolute paths to the description; the task's agents open them by path via Read/Bash.
  async function pickFiles(files: FileList | null) {
    if (!files || files.length === 0) return;
    // crypto.randomUUID 仅在安全上下文可用(https/localhost);经 IP+http 访问时降级。
    if (!draftIdRef.current) {
      draftIdRef.current =
        globalThis.crypto?.randomUUID?.() ?? `d${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;
    }
    setUploading(true);
    try {
      const r = await api.chatUpload("staging", draftIdRef.current, Array.from(files));
      setDescription((prev) => appendUploads(prev, r.attachments));
      setUploadCount((n) => n + r.attachments.length);
    } catch (e) {
      toast.error("上传失败：" + (e as Error).message);
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = ""; // allow re-picking the same file
    }
  }

  // 全局默认策略：建任务时展示"将继承什么"，勾了自定义就用它当编辑起点。
  React.useEffect(() => {
    if (!open) return;
    let alive = true;
    void api
      .explorationPolicyDefault()
      .then((pd) => {
        if (!alive) return;
        setPolicyVocab(pd);
        setPolicyTech([...pd.effective.techniques]);
        setPolicyWrites([...pd.effective.forbidden_writes]);
        setPolicyRules((pd.effective.custom ?? []).map((r) => ({ ...r })));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [open]);

  // 打开抽屉时拉一次平台项目列表(只在 SRC 模式下用到);失败静默——选不到项目不该挡住建任务。
  React.useEffect(() => {
    if (!open) return;
    let alive = true;
    void api
      .srcPrograms()
      .then((r) => {
        if (alive) setSrcPrograms(r.programs ?? []);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [open]);

  // 单任务与批量扇出共用同一份输入，避免两条创建路径的字段逐渐漂移。
  function buildCreateInput() {
    const timeoutSec = Math.max(0, Math.floor(Number(timeoutMin) || 0)) * 60;
    const heartbeatSec = Math.max(10, Math.floor(Number(heartbeatMin) || 10)) * 60; // 下限 10min，与后端归一一致
    return {
      name: name.trim(),
      categoryId: categoryID,
      description: description.trim(),
      goal: goal.trim(),
      llmProfileIds: llmProfileIDs.map(Number),
      sourceTaskIds: sourceTaskIDs,
      companyIds: companyIDs,
      timeoutSeconds: timeoutSec,
      seedFirstIntent,
      planHeartbeatSeconds: heartbeatSec,
      coverageEnabled,
      priority: Math.max(-1000, Math.min(1000, Math.trunc(Number(queuePriority) || 0))),
      mode,
      srcProgramID,
      inheritCoverage: mode === "src" ? inheritCoverage : false,
      modeDoc:
        mode === "src"
          ? {
              asset_token_budget: Math.max(0, Number(assetTokenBudget) || 0),
              asset_time_budget_minutes: Math.max(0, Number(assetTimeBudget) || 0),
              asset_cost_budget_usd: Math.max(0, Number(assetCostBudget) || 0),
              platform_template: platformTemplate || undefined,
            }
          : undefined,
      interceptRules: interceptRules
        .map((r) => ({ ...r, pattern: r.pattern.trim() }))
        .filter((r) => r.pattern !== ""),
      // 勾了自定义才发覆盖层（两轴显式：勾选状态就是本任务的最终策略）；
      // 不勾则整段省略 → 服务端按当时的全局默认落快照。
      explorationPolicy: policyCustom
        ? {
            techniques: policyTech,
            forbidden_writes: policyWrites,
            custom: policyRules.filter((r) => r.pattern.trim() !== ""),
          }
        : undefined,
    };
  }

  function validateCreate(): boolean {
    if (!description.trim() || !goal.trim()) {
      toast.error("请填写描述与目标");
      return false;
    }
    if (sourceTaskIDs.length > MAX_SOURCE_TASKS) {
      toast.error(`最多关联 ${MAX_SOURCE_TASKS} 个来源任务`);
      return false;
    }
    if (batchFanout && companyIDs.length === 0 && sourceTaskIDs.length === 0) {
      toast.error("批量扇出需要先选择关联企业或来源任务，才能确定资产范围");
      return false;
    }
    return true;
  }

  async function previewBatch() {
    if (!validateCreate()) return;
    setCreating(true);
    try {
      const r = await api.createTaskBatch({
        ...buildCreateInput(),
        shardBy,
        assetsPerTask: Math.max(1, Number(assetsPerTask) || 20),
        maxShards: Math.max(1, Number(maxShards) || 50),
        dryRun: true,
      });
      toast.success(`将创建 ${r.totals.shards} 个分片（${r.totals.assets} 个资产）`, {
        description: r.shards
          .slice(0, 5)
          .map((s) => `${s.name}（${s.assets}）`)
          .join(" · "),
      });
    } catch (e) {
      toast.error("预览失败：" + (e as Error).message);
    } finally {
      setCreating(false);
    }
  }

  async function createTask() {
    if (!validateCreate()) return;
    setCreating(true);
    try {
      if (batchFanout) {
        const r = await api.createTaskBatch({
          ...buildCreateInput(),
          shardBy,
          assetsPerTask: Math.max(1, Number(assetsPerTask) || 20),
          maxShards: Math.max(1, Number(maxShards) || 50),
          tokenBudget: Math.max(0, Number(batchTokenBudget) || 0),
          campaignName: campaignName.trim() || undefined,
          campaignTokenBudget: Math.max(0, Number(campaignTokenBudget) || 0),
          onlyNewAssets: batchAssetFilter === "new",
          onlyUntestedAssets: batchAssetFilter === "untested",
        });
        if (r.totals.failed) {
          toast.warning(`已创建 ${r.totals.created ?? 0} 个分片，${r.totals.failed} 个失败`, {
            description: r.error,
          });
        } else {
          toast.success(`已创建 ${r.totals.created ?? 0} 个分片任务`, {
            description: r.campaign ? `campaign：${r.campaign.name}` : undefined,
          });
        }
      } else {
        await api.createTask(buildCreateInput());
        toast.success("任务已创建");
      }
      setName("");
      setCategoryID(undefined);
      setDescription("");
      setGoal("");
      setMode("");
      setSrcProgramID(undefined);
      setInheritCoverage(false);
      setAssetTokenBudget("0");
      setAssetTimeBudget("0");
      setAssetCostBudget("0");
      setPlatformTemplate("");
      setBatchFanout(false);
      setShardBy("host");
      setAssetsPerTask("20");
      setMaxShards("50");
      setBatchTokenBudget("0");
      setCampaignTokenBudget("0");
      setCampaignName("");
      setBatchAssetFilter("all");
      setQueuePriority("0");
      setSelectedTemplateID(null);
      setSourceTaskIDs([]);
      setCompanyIDs([]);
      setLLMProfileIDs([]);
      setTimeoutMin("");
      setHeartbeatMin("10");
      setSeedFirstIntent(false);
      setCoverageEnabled(true);
      setInterceptRules([]);
      setPolicyCustom(false);
      setUploadCount(0);
      draftIdRef.current = "";
      setOpen(false);
      onCreated();
    } catch (e) {
      toast.error("创建失败：" + (e as Error).message);
    } finally {
      setCreating(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <PlusIcon /> 新建任务
        </Button>
      </DialogTrigger>
      {/* 居中弹窗:内容为 flex 列——头/脚固定,中间字段区 flex-1 独立滚动,高度上限 88vh。 */}
      <DialogContent
        ref={sheetContentRef}
        className="flex max-h-[88vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl"
      >
        <DialogHeader className="border-b p-6">
          <DialogTitle>新建任务</DialogTitle>
          <DialogDescription>填写测试对象与目标，高级参数可按需展开。</DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto p-6">
          <div className="grid gap-5">
            <TaskTemplateControls
              description={description}
              goal={goal}
              categoryID={categoryID}
              interceptRules={interceptRules}
              mode={mode}
              srcProgramID={srcProgramID}
              inheritCoverage={inheritCoverage}
              selectedTemplateID={selectedTemplateID}
              onSelectedTemplateIDChange={setSelectedTemplateID}
              onApply={(template) => {
                setDescription(template.description);
                setGoal(template.goal);
                setCategoryID(template.category_id ?? undefined);
                setInterceptRules(template.intercept_rules ?? []);
                setMode(template.mode === "src" ? "src" : "");
                setSrcProgramID(template.mode === "src" && template.src_program_id ? template.src_program_id : undefined);
                setInheritCoverage(template.mode === "src" && !!template.inherit_coverage);
                const batch = template.mode === "src" ? template.batch : null;
                setBatchFanout(!!batch);
                setShardBy(batch?.shard_by ?? "host");
                setAssetsPerTask(batch?.assets_per_task ? String(batch.assets_per_task) : "20");
                setMaxShards(batch?.max_shards ? String(batch.max_shards) : "50");
                setCampaignName(batch?.campaign_name ?? "");
                setCampaignTokenBudget(batch?.campaign_token_budget ? String(batch.campaign_token_budget) : "0");
                setBatchAssetFilter(
                  batch?.only_untested_assets ? "untested" : batch?.only_new_assets ? "new" : "all",
                );
                setUploadCount(0);
              }}
              portalContainer={sheetContentRef}
            />
            <div className="grid gap-2">
              <Label htmlFor="name">名称（可选）</Label>
              <Input
                id="name"
                placeholder="给任务起个便于识别的名字，例如：Acme 官网渗透"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <Field>
              <FieldLabel htmlFor="task-category">任务分类</FieldLabel>
              <CategoryPicker
                categories={categories}
                value={categoryID}
                onValueChange={setCategoryID}
                onCategoryCreated={onCategoriesChanged}
                portalContainer={sheetContentRef}
              />
              <FieldDescription>可选，单个分类；用于任务列表筛选和归档，不影响 Agent 执行。</FieldDescription>
            </Field>
            <Field>
              <FieldLabel>任务模式</FieldLabel>
              {/* 模式创建时定死(与后端的快照语义一致):运行中不可切换,所以只在这里选。 */}
              <div className="inline-flex w-fit rounded-md border p-0.5">
                {[
                  { value: "" as TaskMode, label: "普通渗透" },
                  { value: "src" as TaskMode, label: "SRC 挖掘" },
                ].map((opt) => (
                  <button
                    key={opt.value || "normal"}
                    type="button"
                    aria-label={`任务模式：${opt.label}`}
                    aria-pressed={mode === opt.value}
                    onClick={() => {
                      setMode(opt.value);
                      // 切回普通渗透时清掉项目绑定(普通渗透不吃项目规则,留着会误发)。
                      if (opt.value !== "src") {
                        setSrcProgramID(undefined);
                        setInheritCoverage(false);
                      }
                    }}
                    className={`rounded px-2.5 py-1 text-xs transition-colors ${
                      mode === opt.value
                        ? "bg-primary text-primary-foreground"
                        : "text-muted-foreground hover:bg-muted"
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
              {mode === "src" && (
                <p className="text-muted-foreground text-xs">
                  按「站点 × 风险面」补格：覆盖格没清零前任务不会结束；不追据点/持久化，只做只读证明。
                </p>
              )}
            </Field>
            {mode === "src" && (
              <Field>
                <FieldLabel htmlFor="src-program">平台项目（可选）</FieldLabel>
                <Select
                  value={srcProgramID ? String(srcProgramID) : "none"}
                  onValueChange={(v) => setSrcProgramID(v === "none" ? undefined : Number(v))}
                >
                  <SelectTrigger id="src-program" className="w-full">
                    <SelectValue placeholder="不绑定" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value="none">不绑定</SelectItem>
                      {srcPrograms.map((p) => (
                        <SelectItem key={p.id} value={String(p.id)}>
                          {p.platform ? `${p.name} · ${p.platform}` : p.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  绑定项目后，项目的禁用手段/写操作、限速与必做面会编译进本任务；项目规则改动不影响已建任务。
                </FieldDescription>
              </Field>
            )}
            {mode === "src" && (
              <Field>
                <FieldLabel htmlFor="platform-template">提交块模板</FieldLabel>
                <Select
                  value={platformTemplate || "auto"}
                  onValueChange={(v) => setPlatformTemplate(v === "auto" ? "" : v)}
                >
                  <SelectTrigger id="platform-template">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">自动识别（按平台名）</SelectItem>
                    <SelectItem value="generic">通用</SelectItem>
                    <SelectItem value="butian">补天</SelectItem>
                    <SelectItem value="vulbox">漏洞盒子</SelectItem>
                    <SelectItem value="edu">教育 SRC</SelectItem>
                    <SelectItem value="hackerone">HackerOne</SelectItem>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  只决定【提交信息】块的字段名/顺序；绑定平台项目时以项目规则为准。
                </FieldDescription>
              </Field>
            )}
            {mode === "src" && (
              <Field>
                <FieldLabel htmlFor="asset-token-budget">资产 Token 预算（可选）</FieldLabel>
                <Input
                  id="asset-token-budget"
                  inputMode="numeric"
                  autoComplete="off"
                  className="max-w-xs"
                  placeholder="0 = 跟随系统默认"
                  value={assetTokenBudget}
                  onChange={(e) => setAssetTokenBudget(e.target.value)}
                />
                <FieldDescription>
                  单个资产累计输入+输出 token 的上限（0 = 跟随系统设置的全局默认）。达到上限后不再向该资产派意图，
                  未完成的格自动转 budget_stop，预算转向其他资产。运行中修改全局设置不影响已建任务。
                </FieldDescription>
              </Field>
            )}
            {mode === "src" && (
              <Field>
                <FieldLabel htmlFor="asset-cost-budget">资产成本预算（美元，可选）</FieldLabel>
                <Input
                  id="asset-cost-budget"
                  inputMode="decimal"
                  autoComplete="off"
                  className="max-w-xs"
                  placeholder="0 = 跟随系统默认"
                  value={assetCostBudget}
                  onChange={(e) => setAssetCostBudget(e.target.value)}
                />
                <FieldDescription>
                  单个资产的 LLM 成本上限（0 = 跟随系统设置的全局默认）。到点后新意图被拒绝、
                  未完成格转 budget_stop，预算转向其他资产。
                </FieldDescription>
              </Field>
            )}
            {mode === "src" && (
              <Field>
                <FieldLabel htmlFor="asset-time-budget">资产时间预算（分钟，可选）</FieldLabel>
                <Input
                  id="asset-time-budget"
                  inputMode="numeric"
                  autoComplete="off"
                  className="max-w-xs"
                  placeholder="0 = 跟随系统默认"
                  value={assetTimeBudget}
                  onChange={(e) => setAssetTimeBudget(e.target.value)}
                />
                <FieldDescription>
                  从资产首次产生 LLM 用量起算的墙钟上限（0 = 跟随系统设置的全局默认）。到点后新意图被拒绝、
                  未完成格转 budget_stop，预算转向其他资产；任务本身不会因此收尾。
                </FieldDescription>
              </Field>
            )}
            {mode === "src" && (
              <Field>
                <label htmlFor="inherit-coverage" className="flex items-center gap-2 text-sm">
                  <Checkbox
                    id="inherit-coverage"
                    checked={inheritCoverage}
                    onCheckedChange={(v) => setInheritCoverage(!!v)}
                  />
                  继承近期覆盖（同项目 / 同企业的已测格与收尾数字）
                </label>
                <FieldDescription>
                  只继承本任务范围内资产的 tested_clear 与已完成的收尾数字（标「继承」，证据留在源任务）；
                  不继承发现、N/A 与预算停。重测会覆盖并清除继承标记。默认关闭。
                </FieldDescription>
              </Field>
            )}
            <Field>
              <label htmlFor="batch-fanout" className="flex items-center gap-2 text-sm">
                <Checkbox id="batch-fanout" checked={batchFanout} onCheckedChange={(v) => setBatchFanout(!!v)} />
                批量扇出（按资产分片建多个任务）
              </label>
              <FieldDescription>
                需要先选择关联企业或来源任务来确定资产范围。每个分片只包含本片 host 的 scope，
                归入同一个 campaign；并发仍受全局任务并发上限约束。
              </FieldDescription>
              {batchFanout ? (
                <div className="grid gap-3 rounded-lg border p-3">
                  <div className="grid gap-2 sm:grid-cols-2">
                    <div className="grid gap-1">
                      <Label className="text-xs">分片方式</Label>
                      <Select value={shardBy} onValueChange={(v) => setShardBy(v as typeof shardBy)}>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="host">按 host（推荐）</SelectItem>
                          <SelectItem value="root_domain">按根域名</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div className="grid gap-1">
                      <Label htmlFor="assets-per-task" className="text-xs">
                        每片资产上限
                      </Label>
                      <Input
                        id="assets-per-task"
                        inputMode="numeric"
                        value={assetsPerTask}
                        onChange={(e) => setAssetsPerTask(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-1">
                      <Label htmlFor="max-shards" className="text-xs">
                        最大分片数
                      </Label>
                      <Input
                        id="max-shards"
                        inputMode="numeric"
                        value={maxShards}
                        onChange={(e) => setMaxShards(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-1">
                      <Label htmlFor="batch-token-budget" className="text-xs">
                        每片 token 预算（0=全局）
                      </Label>
                      <Input
                        id="batch-token-budget"
                        inputMode="numeric"
                        value={batchTokenBudget}
                        onChange={(e) => setBatchTokenBudget(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-1">
                      <Label htmlFor="campaign-token-budget" className="text-xs">
                        战役总 token 预算（0=不限）
                      </Label>
                      <Input
                        id="campaign-token-budget"
                        inputMode="numeric"
                        value={campaignTokenBudget}
                        onChange={(e) => setCampaignTokenBudget(e.target.value)}
                      />
                    </div>
                  </div>
                  <p className="text-muted-foreground text-xs">
                    每片预算限制单个分片；战役总预算跨所有分片累计，达到后各分片自动收尾。
                  </p>
                  <div className="grid gap-1">
                    <Label className="text-xs">只跑</Label>
                    <Select
                      value={batchAssetFilter}
                      onValueChange={(v) => setBatchAssetFilter(v as typeof batchAssetFilter)}
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="all">全部资产</SelectItem>
                        <SelectItem value="new">新增资产（未被 SRC 任务关联过）</SelectItem>
                        <SelectItem value="untested">未测资产（没有任何终态格）</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="grid gap-1">
                    <Label htmlFor="campaign-name" className="text-xs">
                      Campaign 名称（可选）
                    </Label>
                    <Input
                      id="campaign-name"
                      value={campaignName}
                      placeholder="留空自动生成"
                      onChange={(e) => setCampaignName(e.target.value)}
                    />
                  </div>
                  <div className="flex items-center gap-2">
                    <Button type="button" variant="outline" size="sm" onClick={() => void previewBatch()} disabled={creating}>
                      预览分片
                    </Button>
                    <span className="text-muted-foreground text-xs">
                      按 host 分组不会被拆开；单个 host 超过每片上限时独占一片。
                    </span>
                  </div>
                </div>
              ) : null}
            </Field>
            <div className="grid gap-2">
              <Label htmlFor="description">描述</Label>
              <Textarea
                id="description"
                className="min-h-32"
                placeholder="测试对象与背景，例如：测试 example.com 这个站点"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
              {/* 上传文件(可多选):暂存到 drafts/,把绝对路径追加进上方描述,worker 据此 Read/Bash 打开。 */}
              <div className="flex flex-wrap items-center gap-2">
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  className="hidden"
                  onChange={(e) => void pickFiles(e.target.files)}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => fileInputRef.current?.click()}
                  disabled={uploading}
                >
                  {uploading ? <Loader2Icon className="animate-spin" /> : <PaperclipIcon />}
                  上传文件
                </Button>
                <span className="text-muted-foreground text-xs">
                  {uploadCount > 0
                    ? `已上传 ${uploadCount} 个文件，绝对路径已追加到描述末尾（可编辑）`
                    : "可多选；上传后把文件的绝对路径追加到描述，供 worker 用 Read/Bash 打开"}
                </span>
              </div>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="goal">目标</Label>
              <Textarea
                id="goal"
                className="min-h-32"
                placeholder="要达成什么，例如：拿下后台管理权限、获取服务器权限"
                value={goal}
                onChange={(e) => setGoal(e.target.value)}
              />
            </div>
            <Field>
              <FieldLabel htmlFor="source-tasks">关联任务</FieldLabel>
              <SourceTaskPicker
                tasks={tasks}
                value={sourceTaskIDs}
                onValueChange={setSourceTaskIDs}
                portalContainer={sheetContentRef}
              />
              <FieldDescription>
                最多关联 {MAX_SOURCE_TASKS}{" "}
                个任务。实时只读继承所选任务的持久化黑板、资产范围及相关流量；新任务写入独立黑板。
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="task-companies">关联企业资产范围</FieldLabel>
              <CompanyPicker
                companies={companies}
                value={companyIDs}
                onValueChange={setCompanyIDs}
                portalContainer={sheetContentRef}
              />
              <FieldDescription>
                创建任务时会将所选企业当前已有资产加入“测试资产”，并将域名、IP、CIDR、ICP 和企业关键词提供给 Agent
                作为范围上下文；不会自动生成意图或强制改变执行目标。
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="task-intercept-rules">任务级资产拦截 / 允许规则（可选）</FieldLabel>
              <AssetInterceptRulesEditor value={interceptRules} onChange={setInterceptRules} />
              <FieldDescription>
                仅对本任务生效，不写入全局规则。判定顺序：先按「拦截」规则（含全局）匹配，命中即禁止测试；未命中且本任务配置了「允许」规则时，须命中某条允许规则才放行，否则同样不允许测试；未配置任何允许规则则不启用白名单。
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="llm-profiles">LLM 配置链</FieldLabel>
              <TaskLLMProfileChain
                profiles={profiles}
                value={llmProfileIDs}
                onValueChange={setLLMProfileIDs}
                inputId="llm-profiles"
                portalContainer={sheetContentRef}
              />
              <FieldDescription>按列表顺序故障转移；第一项为当前配置，仅在明确额度不足时切换下一项。</FieldDescription>
            </Field>

            {/* 高级参数默认折叠:超时/心跳/首个意图,展开才占空间,常用路径保持清爽。 */}
            <Collapsible>
              <CollapsibleTrigger className="group flex w-full items-center gap-2 border-t pt-4 text-sm font-medium">
                <ChevronRightIcon className="text-muted-foreground size-4 transition-transform group-data-[state=open]:rotate-90" />
                高级设置
                <span className="text-muted-foreground ml-auto text-xs font-normal">超时 · 心跳 · 首个意图</span>
              </CollapsibleTrigger>
              <CollapsibleContent className="grid gap-5 pt-5">
                <div className="grid gap-2">
                  <Label htmlFor="timeout-min">任务超时（分钟，可选）</Label>
                  <Input
                    id="timeout-min"
                    type="number"
                    min={0}
                    className="w-40"
                    placeholder="留空 = 不限时"
                    value={timeoutMin}
                    onChange={(e) => setTimeoutMin(e.target.value)}
                  />
                  <p className="text-muted-foreground text-xs">
                    到点后触发优雅收尾（各 agent 写回 + planner 终局判定），任务进入 timeout 终态。
                  </p>
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="heartbeat-min">planner 心跳（分钟）</Label>
                  <Input
                    id="heartbeat-min"
                    type="number"
                    min={10}
                    className="w-40"
                    placeholder="默认 10"
                    value={heartbeatMin}
                    onChange={(e) => setHeartbeatMin(e.target.value)}
                  />
                  <p className="text-muted-foreground text-xs">
                    距上轮规划结束/任务开始满该时长且期间无触发，自动触发一轮规划（兜底卡死 + 唤醒去监督在跑的
                    worker）。下限 10 分钟。
                  </p>
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="queue-priority">排队优先级（越大越先）</Label>
                  <Input
                    id="queue-priority"
                    type="number"
                    className="w-40"
                    min={-1000}
                    max={1000}
                    value={queuePriority}
                    onChange={(e) => setQueuePriority(e.target.value)}
                  />
                  <p className="text-muted-foreground text-xs">
                    只在任务排队等待并发槽时生效（-1000–1000，默认 0）；不会抢占正在运行的任务。
                    批量扇出时每个分片使用同一优先级。
                  </p>
                </div>
                <div className="grid gap-2">
                  <label htmlFor="seed-first-intent" className="flex items-center gap-2 text-sm">
                    <Checkbox
                      id="seed-first-intent"
                      checked={seedFirstIntent}
                      onCheckedChange={(v) => setSeedFirstIntent(!!v)}
                    />
                    直接下发首个意图（描述+目标）
                  </label>
                  <p className="text-muted-foreground text-xs">
                    开启后创建即把「描述+目标」作为一条意图下发，worker 免等首轮规划直接开跑，跑完再由 planner
                    接手判定/补充。CTF 等常一个 work 直接解决的场景推荐开启；关闭则走标准的先规划再执行。
                  </p>
                </div>
                <div className="grid gap-2">
                  <label htmlFor="coverage-enabled" className="flex items-center gap-2 text-sm">
                    <Checkbox
                      id="coverage-enabled"
                      checked={coverageEnabled}
                      onCheckedChange={(v) => setCoverageEnabled(!!v)}
                    />
                    资产覆盖度功能
                  </label>
                  <p className="text-muted-foreground text-xs">
                    默认开启：计算并展示测试覆盖度、态势图显示测试进度、自动累积测试范围。关闭后不再计算/展示覆盖度，
                    态势图仅展示资产不显示进度，agent 也不再获得范围类工具。关闭不影响「关联企业资产范围」。
                  </p>
                </div>
                <div className="grid gap-2">
                  <label htmlFor="policy-custom" className="flex items-center gap-2 text-sm">
                    <Checkbox id="policy-custom" checked={policyCustom} onCheckedChange={(v) => setPolicyCustom(!!v)} />
                    自定义本任务的探索策略（默认继承全局默认）
                  </label>
                  {!policyCustom ? (
                    <p className="text-muted-foreground text-xs">
                      {policyVocab
                        ? `将继承当前全局默认（第 ${policyVocab.version} 版）：禁用 ${
                            policyVocab.effective.techniques?.length ?? 0
                          } 类手段、${policyVocab.effective.forbidden_writes?.length ?? 0} 类敏感写${
                            policyVocab.effective.max_intents_per_round
                              ? `，每轮最多 ${policyVocab.effective.max_intents_per_round} 条意图`
                              : ""
                          }。默认值是快照：之后改全局默认不影响本任务。`
                        : "加载全局默认…"}
                    </p>
                  ) : (
                    <div className="flex flex-col gap-2 rounded-md border border-dashed p-2.5">
                      <span className="text-xs font-medium">禁用的手段（点选切换；未选 = 本任务不禁）</span>
                      <div className="flex flex-wrap gap-1.5">
                        {(policyVocab?.techniques ?? []).map((t) => {
                          const on = policyTech.includes(t.id);
                          return (
                            <button
                              key={t.id}
                              type="button"
                              title={t.hint}
                              onClick={() =>
                                setPolicyTech(on ? policyTech.filter((x) => x !== t.id) : [...policyTech, t.id])
                              }
                              className={`rounded-full border px-2 py-0.5 text-xs transition-colors ${
                                on
                                  ? "border-red-500/40 bg-red-500/15 text-red-600 dark:text-red-400"
                                  : "border-border text-muted-foreground hover:bg-muted"
                              }`}
                            >
                              {on ? "禁止 · " : ""}
                              {t.label}
                            </button>
                          );
                        })}
                      </div>
                      <span className="text-xs font-medium">禁止的敏感写</span>
                      <div className="flex flex-wrap gap-1.5">
                        {(policyVocab?.writes ?? []).map((w) => {
                          const on = policyWrites.includes(w.id);
                          return (
                            <button
                              key={w.id}
                              type="button"
                              title={w.hint}
                              onClick={() =>
                                setPolicyWrites(on ? policyWrites.filter((x) => x !== w.id) : [...policyWrites, w.id])
                              }
                              className={`rounded-full border px-2 py-0.5 text-xs transition-colors ${
                                on
                                  ? "border-red-500/40 bg-red-500/15 text-red-600 dark:text-red-400"
                                  : "border-border text-muted-foreground hover:bg-muted"
                              }`}
                            >
                              {on ? "禁止 · " : ""}
                              {w.label}
                            </button>
                          );
                        })}
                      </div>
                      <span className="text-xs font-medium">自定义禁止条目（词表覆盖不到时手写）</span>
                      <CustomRulesEditor
                        rules={policyRules}
                        onChange={setPolicyRules}
                        limit={policyVocab?.custom_limit ?? 16}
                        patternLimit={policyVocab?.custom_pattern_limit ?? 200}
                      />
                      <p className="text-muted-foreground text-xs">
                        生效方式：不符策略的探索方向不会进探索图（拒绝时说明命中什么、往哪改），同一份策略还会
                        编译成仅本任务生效的拦截规则兜住命令层。这里是加法——平台内置的破坏性拦截始终生效。
                      </p>
                    </div>
                  )}
                </div>
              </CollapsibleContent>
            </Collapsible>
          </div>
        </div>

        <DialogFooter className="flex-row justify-end gap-2 border-t p-4">
          <DialogClose asChild>
            <Button variant="outline">取消</Button>
          </DialogClose>
          <Button onClick={createTask} disabled={creating || uploading}>
            {creating && <Spinner data-icon="inline-start" />}
            {creating ? "创建中" : "创建"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
