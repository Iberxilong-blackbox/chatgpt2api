"use client";

import { useEffect, useMemo, useState } from "react";
import { CheckCircle2, LoaderCircle, Plus, ShieldCheck, Trash2, XCircle } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  createRegistrationIdentity,
  deleteRegistrationIdentity,
  fetchRegistrationIdentities,
  updateRegistrationIdentity,
  type RegistrationIdentity,
} from "@/lib/api";
import { cn } from "@/lib/utils";

import {
  SettingsCard,
  SettingsEmptyState,
  SettingsNotice,
  settingsInputClassName,
  settingsListItemClassName,
} from "./settings-ui";

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
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = async (silent = false) => {
    if (!silent) setIsLoading(true);
    try {
      const data = await fetchRegistrationIdentities();
      setItems(Array.isArray(data.items) ? data.items : []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载注册身份 ID 失败");
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

  const handleAdd = async () => {
    const nextIdentityId = identityId.trim();
    if (!nextIdentityId) {
      toast.error("请输入身份 ID");
      return;
    }
    setIsSaving(true);
    try {
      const data = await createRegistrationIdentity({ identity_id: nextIdentityId, label: label.trim() });
      setItems(data.items || []);
      setIdentityId("");
      setLabel("");
      toast.success("身份 ID 已添加");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "添加身份 ID 失败");
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
      toast.error(error instanceof Error ? error.message : "更新身份 ID 失败");
    } finally {
      setBusyId(null);
    }
  };

  const handleDelete = async (item: RegistrationIdentity) => {
    setBusyId(item.id);
    try {
      const data = await deleteRegistrationIdentity(item.id);
      setItems(data.items || []);
      toast.success("身份 ID 已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除身份 ID 失败");
    } finally {
      setBusyId(null);
    }
  };

  return (
    <SettingsCard
      icon={ShieldCheck}
      title="注册身份 ID"
      description="维护本地注册白名单，原始 ID 仅管理员可见。"
      tone="slate"
      action={
        <Button type="button" variant="outline" size="sm" onClick={() => void load()} disabled={isLoading}>
          {isLoading ? <LoaderCircle className="mr-2 size-4 animate-spin" /> : null}
          刷新
        </Button>
      }
    >
      <div className="flex flex-col gap-4">
        <SettingsNotice>
          用户注册时必须输入未使用的身份 ID。注册成功后 ID 会绑定用户名；即使账号被删除，也不会释放该 ID。
        </SettingsNotice>

        <div className="grid gap-3 md:grid-cols-[minmax(0,1.2fr)_minmax(0,0.8fr)_auto]">
          <Field className="gap-1.5">
            <FieldLabel htmlFor="registration-identity-id">身份 ID</FieldLabel>
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
          <SettingsEmptyState icon={ShieldCheck} title="暂无身份 ID" description="添加后，用户才能通过白名单完成本地注册。" />
        ) : (
          <div className="flex flex-col gap-3">
            {filteredItems.map((item) => {
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

