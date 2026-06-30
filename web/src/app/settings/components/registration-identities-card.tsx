"use client";

import { useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import { CheckCircle2, ChevronLeft, ChevronRight, LoaderCircle, Plus, ShieldCheck, Trash2, Upload, XCircle } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  createRegistrationIdentity,
  deleteRegistrationIdentity,
  fetchRegistrationIdentities,
  importRegistrationIdentities,
  updateRegistrationIdentity,
  type RegistrationIdentity,
  type RegistrationIdentityImportStats,
} from "@/lib/api";
import { cn } from "@/lib/utils";

import {
  SettingsCard,
  SettingsEmptyState,
  SettingsNotice,
  settingsInputClassName,
  settingsListItemClassName,
} from "./settings-ui";

const REGISTRATION_IDENTITIES_PAGE_SIZE = 5;

function formatDate(value?: string) {
  const text = String(value || "").trim();
  if (!text) return "-";
  const date = new Date(text);
  if (Number.isNaN(date.getTime())) return text;
  return date.toLocaleString();
}

export function RegistrationIdentitiesCard() {
  const [items, setItems] = useState<RegistrationIdentity[]>([]);
  const [identityId, setIdentityId] = useState("");
  const [label, setLabel] = useState("");
  const [query, setQuery] = useState("");
  const [currentPage, setCurrentPage] = useState(1);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [isImporting, setIsImporting] = useState(false);
  const [importStats, setImportStats] = useState<RegistrationIdentityImportStats | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const load = async (silent = false) => {
    if (!silent) setIsLoading(true);
    try {
      const data = await fetchRegistrationIdentities();
      setItems(Array.isArray(data.items) ? data.items : []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载好友 ID 失败");
    } finally {
      if (!silent) setIsLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const filteredItems = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return items;
    return items.filter((item) => [
      item.identity_id,
      item.label,
      item.used_by_username,
      item.used_by_user_id,
    ].some((value) => String(value || "").toLowerCase().includes(normalized)));
  }, [items, query]);

  const totalPages = Math.max(1, Math.ceil(filteredItems.length / REGISTRATION_IDENTITIES_PAGE_SIZE));
  const safeCurrentPage = Math.min(currentPage, totalPages);
  const pageStart = (safeCurrentPage - 1) * REGISTRATION_IDENTITIES_PAGE_SIZE;
  const pagedItems = filteredItems.slice(pageStart, pageStart + REGISTRATION_IDENTITIES_PAGE_SIZE);

  useEffect(() => {
    setCurrentPage(1);
  }, [query]);

  const handleAdd = async () => {
    const nextIdentityId = identityId.trim();
    if (!nextIdentityId) {
      toast.error("请输入好友 ID");
      return;
    }
    setIsSaving(true);
    try {
      const data = await createRegistrationIdentity({ identity_id: nextIdentityId, label: label.trim() });
      setItems(data.items || []);
      setCurrentPage(1);
      setIdentityId("");
      setLabel("");
      toast.success("好友 ID 已添加");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "添加好友 ID 失败");
    } finally {
      setIsSaving(false);
    }
  };

  const handleToggle = async (item: RegistrationIdentity) => {
    setBusyId(item.id);
    try {
      const data = await updateRegistrationIdentity(item.id, { enabled: !item.enabled });
      setItems(data.items || []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新好友 ID 失败");
    } finally {
      setBusyId(null);
    }
  };

  const handleDelete = async (item: RegistrationIdentity) => {
    setBusyId(item.id);
    try {
      const data = await deleteRegistrationIdentity(item.id);
      setItems(data.items || []);
      toast.success("好友 ID 已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除好友 ID 失败");
    } finally {
      setBusyId(null);
    }
  };

  const handleImportFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    setIsImporting(true);
    try {
      const document = JSON.parse(await file.text()) as unknown;
      const data = await importRegistrationIdentities(document, label.trim());
      setItems(data.items || []);
      setCurrentPage(1);
      setImportStats(data.stats);
      toast.success(`导入完成：新增 ${data.stats.added}，跳过 ${data.stats.duplicates}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "导入好友 ID JSON 失败");
    } finally {
      setIsImporting(false);
    }
  };

  return (
    <SettingsCard
      icon={ShieldCheck}
      title="注册好友 ID"
      description="维护本地注册白名单，原始好友 ID 仅管理员可见。"
      tone="slate"
      action={
        <div className="flex flex-wrap justify-end gap-2">
          <input
            ref={fileInputRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={(event) => void handleImportFile(event)}
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => fileInputRef.current?.click()}
            disabled={isImporting}
          >
            {isImporting ? <LoaderCircle className="mr-2 size-4 animate-spin" /> : <Upload className="mr-2 size-4" />}
            导入 JSON
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => void load()} disabled={isLoading}>
            {isLoading ? <LoaderCircle className="mr-2 size-4 animate-spin" /> : null}
            刷新
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <SettingsNotice>
          用户注册时必须输入未使用的好友 ID。注册成功后 ID 会绑定用户名；即使账号被删除，也不会释放该 ID。
        </SettingsNotice>

        {importStats ? (
          <div className="grid gap-2 rounded-md border border-border/70 bg-muted/30 p-3 text-xs text-muted-foreground sm:grid-cols-5">
            <span>输入 {importStats.input}</span>
            <span>原有 {importStats.existing}</span>
            <span>新增 {importStats.added}</span>
            <span>跳过 {importStats.duplicates}</span>
            <span>无效 {importStats.invalid_rows}</span>
          </div>
        ) : null}

        <div className="grid gap-3 md:grid-cols-[minmax(0,1.2fr)_minmax(0,0.8fr)_auto]">
          <Field className="gap-1.5">
            <FieldLabel htmlFor="registration-identity-id">好友 ID</FieldLabel>
            <Input
              id="registration-identity-id"
              value={identityId}
              onChange={(event) => setIdentityId(event.target.value)}
              placeholder="输入好友 ID"
              className={settingsInputClassName}
            />
          </Field>
          <Field className="gap-1.5">
            <FieldLabel htmlFor="registration-identity-label">备注</FieldLabel>
            <Input
              id="registration-identity-label"
              value={label}
              onChange={(event) => setLabel(event.target.value)}
              placeholder="可选"
              className={settingsInputClassName}
            />
          </Field>
          <Button type="button" className="self-end" onClick={() => void handleAdd()} disabled={isSaving}>
            {isSaving ? <LoaderCircle className="mr-2 size-4 animate-spin" /> : <Plus className="mr-2 size-4" />}
            添加
          </Button>
        </div>

        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="搜索 ID、备注、用户名或用户 ID"
          className={settingsInputClassName}
        />

        {isLoading ? (
          <div className="flex items-center justify-center py-10 text-muted-foreground">
            <LoaderCircle className="mr-2 size-4 animate-spin" />
            加载中
          </div>
        ) : filteredItems.length === 0 ? (
          <SettingsEmptyState icon={ShieldCheck} title="暂无好友 ID" description="添加后，用户才能通过白名单完成本地注册。" />
        ) : (
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border/70 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
              <span>
                共 {filteredItems.length} 条，显示 {pageStart + 1}-{Math.min(pageStart + pagedItems.length, filteredItems.length)} 条
              </span>
              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setCurrentPage((page) => Math.max(1, page - 1))}
                  disabled={safeCurrentPage <= 1}
                >
                  <ChevronLeft className="mr-1 size-4" />
                  上一页
                </Button>
                <span className="min-w-14 text-center text-foreground">
                  {safeCurrentPage}/{totalPages}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setCurrentPage((page) => Math.min(totalPages, page + 1))}
                  disabled={safeCurrentPage >= totalPages}
                >
                  下一页
                  <ChevronRight className="ml-1 size-4" />
                </Button>
              </div>
            </div>
            {pagedItems.map((item) => {
              const used = Boolean(item.used);
              const enabled = Boolean(item.enabled);
              const busy = busyId === item.id;
              return (
                <div key={item.id} className={cn(settingsListItemClassName, "flex flex-col gap-3")}>
                  <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0 space-y-1">
                      <div className="break-all font-mono text-sm font-semibold text-foreground">{item.identity_id}</div>
                      <div className="text-xs text-muted-foreground">{item.label || "未备注"}</div>
                    </div>
                    <div className="flex shrink-0 flex-wrap gap-2">
                      <Badge variant={used ? "secondary" : "outline"}>{used ? "已使用" : "未使用"}</Badge>
                      <Badge variant={enabled ? "default" : "danger"}>{enabled ? "已启用" : "已禁用"}</Badge>
                      {item.used_user_deleted ? <Badge variant="danger">账号已删除</Badge> : null}
                    </div>
                  </div>

                  <div className="grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
                    <div>绑定用户名：<span className="text-foreground">{item.used_by_username || "-"}</span></div>
                    <div>绑定用户 ID：<span className="text-foreground">{item.used_by_user_id || "-"}</span></div>
                    <div>使用时间：<span className="text-foreground">{formatDate(item.used_at)}</span></div>
                    <div>创建时间：<span className="text-foreground">{formatDate(item.created_at)}</span></div>
                  </div>

                  <div className="flex flex-wrap justify-end gap-2">
                    <Button type="button" variant="outline" size="sm" onClick={() => void handleToggle(item)} disabled={busy}>
                      {enabled ? <XCircle className="mr-2 size-4" /> : <CheckCircle2 className="mr-2 size-4" />}
                      {enabled ? "禁用" : "启用"}
                    </Button>
                    <Button type="button" variant="destructive" size="sm" onClick={() => void handleDelete(item)} disabled={busy || used}>
                      {busy ? <LoaderCircle className="mr-2 size-4 animate-spin" /> : <Trash2 className="mr-2 size-4" />}
                      删除
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </SettingsCard>
  );
}

