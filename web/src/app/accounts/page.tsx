"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ComponentProps } from "react";
import {
  Ban,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  CircleOff,
  Copy,
  Download,
  Flame,
  LoaderCircle,
  Pencil,
  Play,
  RefreshCw,
  Search,
  Square,
  Trash2,
  UserRound,
  Stethoscope,
} from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  deleteAccounts,
  diagnoseAccount,
  type DiagnoseResult,
  fetchAccountTokens,
  fetchAccountReservoir,
  pauseAccountReservoir,
  refillAccountReservoir,
  resumeAccountReservoir,
  fetchAccounts,
  getWarmingStatus,
  refreshAccounts,
  startWarming,
  stopWarming,
  updateAccount,
  type Account,
  type AccountStatus,
  type AccountType,
  type ReservoirSnapshot,
  type WarmingStatus,
} from "@/lib/api";
import { useAuthGuard } from "@/lib/use-auth-guard";
import { cn } from "@/lib/utils";
import { hasAPIPermission, type StoredAuthSession } from "@/store/auth";

import { AccountImportDialog } from "./components/account-import-dialog";

const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";

const accountTypeOptions: { label: string; value: AccountType | "all" }[] = [
  { label: "全部类型", value: "all" },
  { label: "Free", value: "Free" },
  { label: "Plus", value: "Plus" },
  { label: "ProLite", value: "ProLite" },
  { label: "Team", value: "Team" },
  { label: "Pro", value: "Pro" },
];

const accountStatusOptions: { label: string; value: AccountStatus | "all" }[] = [
  { label: "全部状态", value: "all" },
  { label: "正常", value: "正常" },
  { label: "限流", value: "限流" },
  { label: "异常", value: "异常" },
  { label: "刷新中", value: "刷新中" },
  { label: "过期待刷新", value: "过期待刷新" },
  { label: "禁用", value: "禁用" },
];

type WarmingFilter = "all" | "none" | "warming" | "done" | "failed";

const warmingFilterOptions: { label: string; value: WarmingFilter }[] = [
  { label: "全部养号", value: "all" },
  { label: "未养号", value: "none" },
  { label: "养号中", value: "warming" },
  { label: "已养熟", value: "done" },
  { label: "失败 >= 3", value: "failed" },
];

const statusMeta: Record<
  AccountStatus,
  {
    icon: typeof CheckCircle2;
    badge: ComponentProps<typeof Badge>["variant"];
  }
> = {
  正常: { icon: CheckCircle2, badge: "success" },
  限流: { icon: CircleAlert, badge: "warning" },
  异常: { icon: CircleOff, badge: "danger" },
  刷新中: { icon: LoaderCircle, badge: "warning" },
  过期待刷新: { icon: CircleAlert, badge: "warning" },
  禁用: { icon: Ban, badge: "secondary" },
};

const refreshErrorStageLabels: Record<string, string> = {
  bootstrap: "站点初始化",
  me: "账号身份验证",
  conversation_init: "额度初始化",
  session_refresh: "Session 刷新",
  remote_info: "远端账号验证",
};

const metricCards = [
  {
    key: "total",
    label: "账户总数",
    description: "池内全部账号",
    icon: UserRound,
    iconClassName: "bg-stone-100 text-stone-600",
  },
  {
    key: "active",
    label: "正常",
    description: "可用于调度",
    icon: CheckCircle2,
    iconClassName: "bg-emerald-50 text-emerald-700 ring-1 ring-emerald-100",
  },
  {
    key: "limited",
    label: "限流",
    description: "等待额度恢复",
    icon: CircleAlert,
    iconClassName: "bg-amber-50 text-amber-700 ring-1 ring-amber-100",
  },
  {
    key: "abnormal",
    label: "异常",
    description: "建议刷新或移除",
    icon: CircleOff,
    iconClassName: "bg-rose-50 text-rose-700 ring-1 ring-rose-100",
  },
  {
    key: "disabled",
    label: "禁用",
    description: "不会参与调度",
    icon: Ban,
    iconClassName: "bg-stone-100 text-stone-500",
  },
  {
    key: "quota",
    label: "账面额度",
    description: "正常账号额度合计",
    icon: RefreshCw,
    iconClassName: "bg-[#edf4ff] text-[#1456f0] ring-1 ring-blue-100",
  },
] as const;

function isUnlimitedImageQuotaAccount(account: Account) {
  return account.type === "Pro" || account.type === "ProLite";
}

function formatCompact(value: number) {
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}k`;
  }
  return String(value);
}

function formatQuota(account: Account) {
  if (isUnlimitedImageQuotaAccount(account)) {
    return "∞";
  }
  if (account.imageQuotaUnknown) {
    return "未知";
  }
  return String(Math.max(0, account.quota));
}

function formatRestoreAt(value: string | null | undefined, mode: "restore" | "reset") {
  if (!value) {
    return { absolute: "—", relative: "" };
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return { absolute: value, relative: "" };
  }

  const diffMs = Math.max(0, date.getTime() - Date.now());
  const totalHours = Math.ceil(diffMs / (1000 * 60 * 60));
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  const reachedText = mode === "restore" ? "已到恢复时间" : "已到重置时间";
  const relative = diffMs > 0 ? `剩余 ${days}d ${hours}h` : reachedText;

  const pad = (num: number) => String(num).padStart(2, "0");
  const absolute = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;

  return { absolute, relative };
}

function isToday(value?: string | null) {
  if (!value) return false;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return false;
  const now = new Date();
  return (
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
  );
}

function matchesWarmingFilter(account: Account, filter: WarmingFilter) {
  switch (filter) {
    case "none":
      return !account.warmingStatus;
    case "warming":
      return account.warmingStatus === "warming";
    case "done":
      return account.warmingStatus === "done";
    case "failed":
      return (account.warmingErrors ?? 0) >= 3;
    default:
      return true;
  }
}

function formatQuotaSummary(accounts: Account[]) {
  const availableAccounts = accounts.filter((account) => account.status === "正常");
  if (availableAccounts.some(isUnlimitedImageQuotaAccount)) {
    return "∞";
  }
  if (availableAccounts.some((account) => account.imageQuotaUnknown)) {
    return "未知";
  }
  return formatCompact(availableAccounts.reduce((sum, account) => sum + Math.max(0, account.quota), 0));
}

function maskToken(token?: string) {
  if (!token) return "—";
  if (token.length <= 18) return token;
  return `${token.slice(0, 16)}...${token.slice(-8)}`;
}

function accountTokenLabel(account: Account) {
  return maskToken(account.access_token || account.token_preview || account.id);
}

function accountPrimaryLabel(account: Account) {
  return account.email?.trim() || account.user_id?.trim() || "未识别账号";
}

function accountSecondaryLabel(account: Account) {
  return accountTokenLabel(account);
}

const reservoirLayerLabels: Record<string, string> = {
  available_with_quota: "有额度可用",
  available_unknown_quota: "已验证未知额度",
  unverified_imported: "未验证导入",
  empty_waiting_restore: "等待恢复",
  restore_due: "到期待刷新",
  stale_verified: "信息过旧",
  long_unrefreshed: "超3天未刷新",
  zero_quota_recheck_due: "0额度待复查",
  zero_quota_rechecked: "连续0额度",
  refreshing: "刷新中",
  invalid_or_disabled: "异常/禁用",
};

function formatReservoirTime(value?: string | null) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const minutes = Math.max(1, Math.ceil((date.getTime() - Date.now()) / 60000));
  if (minutes < 60) return `${minutes} 分钟后`;
  const hours = Math.ceil(minutes / 60);
  return `${hours} 小时后`;
}

function formatDurationSeconds(value?: number) {
  if (!Number.isFinite(value ?? NaN) || !value || value <= 0) return "—";
  if (value < 60) return `${Math.round(value)}秒`;
  const minutes = Math.floor(value / 60);
  const seconds = Math.round(value % 60);
  if (minutes < 60) return seconds > 0 ? `${minutes}分${seconds}秒` : `${minutes}分`;
  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  return restMinutes > 0 ? `${hours}小时${restMinutes}分` : `${hours}小时`;
}

function formatDurationMs(value?: number) {
  if (!Number.isFinite(value ?? NaN) || !value || value <= 0) return "—";
  if (value < 1000) return `${Math.round(value)}ms`;
  return formatDurationSeconds(value / 1000);
}
function formatForecastHour(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const hours = String(date.getHours()).padStart(2, "0");
  return `${hours}:00`;
}

function reservoirRiskLabel(value?: string) {
  if (value === "danger") return "危险";
  if (value === "warning") return "偏低";
  return "正常";
}

function reservoirRiskClassName(value?: string) {
  if (value === "danger") return "border-red-200 bg-red-50 text-red-700";
  if (value === "warning") return "border-amber-200 bg-amber-50 text-amber-700";
  return "border-emerald-200 bg-emerald-50 text-emerald-700";
}

function downloadTokenFile(tokens: string[]) {
  const content = `${tokens.join("\n")}\n`;
  const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `accounts-${Date.now()}.txt`;
  link.click();
  URL.revokeObjectURL(url);
}

function copyToClipboard(text: string, successMessage: string) {
  if (!text) {
    return;
  }
  void navigator.clipboard.writeText(text).then(
    () => toast.success(successMessage),
    () => toast.error("复制失败"),
  );
}

function normalizeAccounts(items: Account[] | null | undefined): Account[] {
  const accountItems = Array.isArray(items) ? items : [];
  return accountItems.map((item) => ({
    ...item,
    type:
      item.type === "Plus" ||
      item.type === "ProLite" ||
      item.type === "Team" ||
      item.type === "Pro" ||
      item.type === "Free"
        ? item.type
        : "Free",
  }));
}

function AccountsPageContent({ session }: { session: StoredAuthSession }) {
  const didLoadRef = useRef(false);
  const warmingSnapshotRef = useRef({ running: false, processed: 0 });
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState<AccountType | "all">("all");
  const [statusFilter, setStatusFilter] = useState<AccountStatus | "all">("all");
  const [warmingFilter, setWarmingFilter] = useState<WarmingFilter>("all");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState("10");
  const [editingAccount, setEditingAccount] = useState<Account | null>(null);
  const [editType, setEditType] = useState<AccountType>("Free");
  const [editStatus, setEditStatus] = useState<AccountStatus>("正常");
  const [editQuota, setEditQuota] = useState("0");
  const [editWarmingStatus, setEditWarmingStatus] = useState<string>("");
  const [editWarmingDay, setEditWarmingDay] = useState("0");
  const [isLoading, setIsLoading] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [isUpdating, setIsUpdating] = useState(false);
  const [isBulkUpdatingWarming, setIsBulkUpdatingWarming] = useState(false);
  const [isExporting, setIsExporting] = useState(false);
  const [isDiagnosing, setIsDiagnosing] = useState(false);
  const [warmingStatus, setWarmingStatus] = useState<WarmingStatus>({
    running: false,
    processed: 0,
    total: 0,
  });
  const [isWarmingStatusLoading, setIsWarmingStatusLoading] = useState(false);
  const [isWarmingStarting, setIsWarmingStarting] = useState(false);
  const [isWarmingStopping, setIsWarmingStopping] = useState(false);
  const [diagnoseResult, setDiagnoseResult] = useState<DiagnoseResult | null>(null);
  const [diagnoseAccountId, setDiagnoseAccountId] = useState<string | null>(null);
  const [refreshingAccountIds, setRefreshingAccountIds] = useState<string[]>([]);
  const [reservoirSnapshot, setReservoirSnapshot] = useState<ReservoirSnapshot | null>(null);
  const [isReservoirLoading, setIsReservoirLoading] = useState(false);
  const [isReservoirActionRunning, setIsReservoirActionRunning] = useState(false);

  const canImportTokenAccounts = hasAPIPermission(session, "POST", "/api/accounts");
  const canImportSessionAccounts = hasAPIPermission(session, "POST", "/api/accounts/session");
  const canImportAccounts = canImportTokenAccounts || canImportSessionAccounts;
  const canRefreshAccounts = hasAPIPermission(session, "POST", "/api/accounts/refresh");
  const canUpdateAccount = hasAPIPermission(session, "POST", "/api/accounts/update");
  const canDeleteAccounts = hasAPIPermission(session, "DELETE", "/api/accounts");
  const canExportTokens = hasAPIPermission(session, "GET", "/api/accounts/tokens");
  const canViewReservoir = hasAPIPermission(session, "GET", "/api/accounts/reservoir");
  const canRefillReservoir = hasAPIPermission(session, "POST", "/api/accounts/reservoir/refill");
  const canPauseReservoir = hasAPIPermission(session, "POST", "/api/accounts/reservoir/pause");
  const canResumeReservoir = hasAPIPermission(session, "POST", "/api/accounts/reservoir/resume");
  const canViewWarmingStatus = hasAPIPermission(session, "GET", "/api/accounts/warming/status");
  const canStartWarming = hasAPIPermission(session, "POST", "/api/accounts/warming/start");
  const canStopWarming = hasAPIPermission(session, "POST", "/api/accounts/warming/stop");

  const applyAccountItems = useCallback((items: Account[] | null | undefined) => {
    const nextAccounts = normalizeAccounts(items);
    setAccounts(nextAccounts);
    setSelectedIds((prev) => prev.filter((id) => nextAccounts.some((item) => item.id === id)));
    return nextAccounts;
  }, []);

  const loadAccounts = useCallback(async (silent = false) => {
    if (!silent) {
      setIsLoading(true);
    }
    try {
      const data = await fetchAccounts();
      applyAccountItems(data.items);
    } catch (error) {
      const message = error instanceof Error ? error.message : "加载账户失败";
      toast.error(message);
    } finally {
      if (!silent) {
        setIsLoading(false);
      }
    }
  }, [applyAccountItems]);

  const loadReservoir = useCallback(async (silent = false) => {
    if (!canViewReservoir) {
      return;
    }
    if (!silent) {
      setIsReservoirLoading(true);
    }
    try {
      const snapshot = await fetchAccountReservoir();
      setReservoirSnapshot(snapshot);
    } catch (error) {
      const message = error instanceof Error ? error.message : "加载蓄水池状态失败";
      if (!silent) {
        toast.error(message);
      }
    } finally {
      if (!silent) {
        setIsReservoirLoading(false);
      }
    }
  }, [canViewReservoir]);
  const loadWarmingStatus = useCallback(async (silent = false) => {
    if (!canViewWarmingStatus) {
      return;
    }
    if (!silent) {
      setIsWarmingStatusLoading(true);
    }
    try {
      const data = await getWarmingStatus();
      const previous = warmingSnapshotRef.current;
      const shouldRefreshAccounts =
        previous.processed !== data.status.processed || (previous.running && !data.status.running);
      warmingSnapshotRef.current = {
        running: data.status.running,
        processed: data.status.processed,
      };
      setWarmingStatus(data.status);
      if (shouldRefreshAccounts) {
        void loadAccounts(true);
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "加载养号状态失败";
      if (!silent) {
        toast.error(message);
      }
    } finally {
      if (!silent) {
        setIsWarmingStatusLoading(false);
      }
    }
  }, [canViewWarmingStatus, loadAccounts]);

  useEffect(() => {
    if (didLoadRef.current) {
      return;
    }
    didLoadRef.current = true;
    void loadAccounts();
  }, [loadAccounts]);

  useEffect(() => {
    void loadReservoir(true);
  }, [loadReservoir]);

  useEffect(() => {
    if (!canViewReservoir) {
      return;
    }
    const timer = window.setInterval(() => {
      void loadReservoir(true);
    }, 10000);
    return () => window.clearInterval(timer);
  }, [canViewReservoir, loadReservoir]);
  useEffect(() => {
    void loadWarmingStatus(true);
  }, [loadWarmingStatus]);

  useEffect(() => {
    if (!canViewWarmingStatus || !warmingStatus.running) {
      return;
    }
    const timer = window.setInterval(() => {
      void loadWarmingStatus(true);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [canViewWarmingStatus, loadWarmingStatus, warmingStatus.running]);

  const filteredAccounts = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    return accounts.filter((account) => {
      const searchMatched =
        normalizedQuery.length === 0 || (account.email ?? "").toLowerCase().includes(normalizedQuery);
      const typeMatched = typeFilter === "all" || account.type === typeFilter;
      const statusMatched = statusFilter === "all" || account.status === statusFilter;
      const warmingMatched = matchesWarmingFilter(account, warmingFilter);
      return searchMatched && typeMatched && statusMatched && warmingMatched;
    });
  }, [accounts, query, statusFilter, typeFilter, warmingFilter]);

  const pageCount = Math.max(1, Math.ceil(filteredAccounts.length / Number(pageSize)));
  const safePage = Math.min(page, pageCount);
  const startIndex = (safePage - 1) * Number(pageSize);
  const currentRows = filteredAccounts.slice(startIndex, startIndex + Number(pageSize));
  const selectedIdSet = useMemo(() => new Set(selectedIds), [selectedIds]);
  const allCurrentSelected =
    currentRows.length > 0 && currentRows.every((row) => selectedIdSet.has(row.id));
  const someCurrentSelected = currentRows.some((row) => selectedIdSet.has(row.id));
  const currentSelectState: boolean | "indeterminate" = allCurrentSelected
    ? true
    : someCurrentSelected
      ? "indeterminate"
      : false;
  const allFilteredSelected =
    filteredAccounts.length > 0 && filteredAccounts.every((row) => selectedIdSet.has(row.id));
  const allAccountsSelected =
    accounts.length > 0 && accounts.every((row) => selectedIdSet.has(row.id));
  const showInitialEmptyState = !isLoading && accounts.length === 0;
  const showFilteredEmptyState = !isLoading && accounts.length > 0 && currentRows.length === 0;

  const summary = useMemo(() => {
    const total = accounts.length;
    const active = accounts.filter((item) => item.status === "正常").length;
    const limited = accounts.filter((item) => item.status === "限流").length;
    const abnormal = accounts.filter((item) => item.status === "异常").length;
    const disabled = accounts.filter((item) => item.status === "禁用").length;
    const quota = formatQuotaSummary(accounts);

    return { total, active, limited, abnormal, disabled, quota };
  }, [accounts]);

  const selectedAccountIds = useMemo(() => {
    const selectedSet = new Set(selectedIds);
    return accounts.filter((item) => selectedSet.has(item.id)).map((item) => item.id);
  }, [accounts, selectedIds]);

  const selectedFilteredAccountIds = useMemo(() => {
    const selectedSet = new Set(selectedIds);
    return filteredAccounts.filter((item) => selectedSet.has(item.id)).map((item) => item.id);
  }, [filteredAccounts, selectedIds]);

  const abnormalAccountIds = useMemo(() => {
    return accounts.filter((item) => item.status === "异常").map((item) => item.id);
  }, [accounts]);

  const refreshingAccountIdSet = useMemo(() => new Set(refreshingAccountIds), [refreshingAccountIds]);

  const reservoirForecastPoints = useMemo(() => {
    const points = reservoirSnapshot?.forecast ?? [];
    return points.filter((_, index) => index < 6 || (index + 1) % 4 === 0).slice(0, 10);
  }, [reservoirSnapshot?.forecast]);

  const reservoirForecastMinWater = useMemo(() => {
    const points = reservoirSnapshot?.forecast ?? [];
    if (points.length === 0) return null;
    return Math.min(...points.map((point) => point.estimatedWater));
  }, [reservoirSnapshot?.forecast]);

  const reservoirForecastRisk = useMemo(() => {
    const points = reservoirSnapshot?.forecast ?? [];
    if (points.some((point) => point.riskLevel === "danger")) return "danger";
    if (points.some((point) => point.riskLevel === "warning")) return "warning";
    return points.length > 0 ? "normal" : null;
  }, [reservoirSnapshot?.forecast]);

  const paginationItems = useMemo(() => {
    const items: (number | "...")[] = [];
    const start = Math.max(1, safePage - 1);
    const end = Math.min(pageCount, safePage + 1);

    if (start > 1) items.push(1);
    if (start > 2) items.push("...");
    for (let current = start; current <= end; current += 1) items.push(current);
    if (end < pageCount - 1) items.push("...");
    if (end < pageCount) items.push(pageCount);

    return items;
  }, [pageCount, safePage]);

  const handleDeleteAccounts = async (accountIds: string[]) => {
    if (!canDeleteAccounts) {
      toast.error("没有删除账号权限");
      return;
    }
    if (accountIds.length === 0) {
      toast.error("请先选择要删除的账户");
      return;
    }

    setIsDeleting(true);
    try {
      const data = await deleteAccounts(accountIds);
      applyAccountItems(data.items);
      toast.success(`删除 ${data.removed ?? 0} 个账户`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "删除账户失败";
      toast.error(message);
    } finally {
      setIsDeleting(false);
    }
  };

  const handleReservoirRefill = async () => {
    if (!canRefillReservoir) {
      toast.error("没有触发补水权限");
      return;
    }
    setIsReservoirActionRunning(true);
    try {
      const snapshot = await refillAccountReservoir();
      setReservoirSnapshot(snapshot);
      void loadAccounts(true);
      toast.success("已触发蓄水池补水");
    } catch (error) {
      const message = error instanceof Error ? error.message : "触发补水失败";
      toast.error(message);
    } finally {
      setIsReservoirActionRunning(false);
    }
  };

  const handleReservoirPauseToggle = async () => {
    const paused = reservoirSnapshot?.paused ?? false;
    if (paused && !canResumeReservoir) {
      toast.error("没有恢复调度权限");
      return;
    }
    if (!paused && !canPauseReservoir) {
      toast.error("没有暂停调度权限");
      return;
    }
    setIsReservoirActionRunning(true);
    try {
      const snapshot = paused ? await resumeAccountReservoir() : await pauseAccountReservoir();
      setReservoirSnapshot(snapshot);
      toast.success(paused ? "已恢复蓄水池调度" : "已暂停蓄水池调度");
    } catch (error) {
      const message = error instanceof Error ? error.message : "更新调度状态失败";
      toast.error(message);
    } finally {
      setIsReservoirActionRunning(false);
    }
  };
  const handleRefreshAccounts = async (accountIds: string[]) => {
    if (!canRefreshAccounts) {
      toast.error("没有刷新账号权限");
      return;
    }
    const targetIds = Array.from(new Set(accountIds.map((id) => id.trim()).filter(Boolean)));
    if (targetIds.length === 0) {
      toast.error("没有需要刷新的账户");
      return;
    }

    setIsRefreshing(true);
    setRefreshingAccountIds(targetIds);
    try {
      const data = await refreshAccounts(targetIds);
      applyAccountItems(data.items);
      window.dispatchEvent(new Event(QUOTA_REFRESH_EVENT));
      const sessionValidationSucceeded = Math.max(
        0,
        (data.session_refreshed ?? 0) - (data.session_validation_failed ?? 0),
      );
      const verifiedCount = data.refreshed + sessionValidationSucceeded;
      if (data.errors.length > 0) {
        const firstError = data.errors[0]?.error;
        toast.error(
          `验证成功 ${verifiedCount} 个，失败 ${data.errors.length} 个${firstError ? `，首个错误：${firstError}` : ""}`,
        );
      } else {
        toast.success(`验证成功 ${verifiedCount} 个账户`);
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "刷新账户失败";
      toast.error(message);
    } finally {
      setIsRefreshing(false);
      setRefreshingAccountIds([]);
    }
  };

  const handleExportTokens = async () => {
    if (!canExportTokens) {
      toast.error("没有导出 Token 权限");
      return;
    }
    setIsExporting(true);
    try {
      const data = await fetchAccountTokens();
      const tokens = (Array.isArray(data.tokens) ? data.tokens : [])
        .map((item) => String(item || "").trim())
        .filter(Boolean);
      if (tokens.length === 0) {
        toast.error("暂无可导出的 Token");
        return;
      }
      downloadTokenFile(tokens);
      toast.success(`已导出 ${tokens.length} 个 Token`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "导出 Token 失败";
      toast.error(message);
    } finally {
      setIsExporting(false);
    }
  };

  const handleDiagnose = async (accountId: string) => {
    setDiagnoseAccountId(accountId);
    setDiagnoseResult(null);
    setIsDiagnosing(true);
    try {
      const result = await diagnoseAccount(accountId);
      setDiagnoseResult(result);
    } catch (error) {
      const message = error instanceof Error ? error.message : "诊断失败";
      toast.error(message);
    } finally {
      setIsDiagnosing(false);
    }
  };

  const openEditDialog = (account: Account) => {
    if (!canUpdateAccount) {
      return;
    }
    setEditingAccount(account);
    setEditType(account.type);
    setEditStatus(account.status);
    setEditQuota(String(account.quota));
    setEditWarmingStatus(account.warmingStatus ?? "");
    setEditWarmingDay(String(account.warmingDay ?? 0));
  };

  const handleUpdateAccount = async () => {
    if (!editingAccount || !canUpdateAccount) {
      return;
    }

    setIsUpdating(true);
    try {
      const data = await updateAccount(editingAccount.id, {
        type: editType,
        status: editStatus,
        quota: Number(editQuota || 0),
        warming_status: editWarmingStatus || null,
        warming_day: Number(editWarmingDay || 0),
      });
      applyAccountItems(data.items);
      setEditingAccount(null);
      toast.success("账号信息已更新");
    } catch (error) {
      const message = error instanceof Error ? error.message : "更新账号失败";
      toast.error(message);
    } finally {
      setIsUpdating(false);
    }
  };

  const handleStartWarming = async () => {
    if (!canStartWarming) {
      toast.error("没有启动养号权限");
      return;
    }
    setIsWarmingStarting(true);
    try {
      const data = await startWarming();
      warmingSnapshotRef.current = {
        running: data.status.running,
        processed: data.status.processed,
      };
      setWarmingStatus(data.status);
      toast.success("养号任务已启动");
    } catch (error) {
      const message = error instanceof Error ? error.message : "启动养号失败";
      toast.error(message);
    } finally {
      setIsWarmingStarting(false);
    }
  };

  const handleStopWarming = async () => {
    if (!canStopWarming) {
      toast.error("没有停止养号权限");
      return;
    }
    setIsWarmingStopping(true);
    try {
      const data = await stopWarming();
      warmingSnapshotRef.current = {
        running: data.status.running,
        processed: data.status.processed,
      };
      setWarmingStatus(data.status);
      toast.success("已请求停止养号任务");
    } catch (error) {
      const message = error instanceof Error ? error.message : "停止养号失败";
      toast.error(message);
    } finally {
      setIsWarmingStopping(false);
    }
  };

  const handleBulkUpdateWarming = async (
    accountIds: string[],
    updates: { warming_status: string | null; warming_day?: number },
    successLabel: string,
  ) => {
    if (!canUpdateAccount) {
      toast.error("没有更新账号权限");
      return;
    }
    const targetIds = Array.from(new Set(accountIds.map((id) => id.trim()).filter(Boolean)));
    if (targetIds.length === 0) {
      toast.error("请先选择当前筛选结果中的账号");
      return;
    }

    setIsBulkUpdatingWarming(true);
    try {
      let latestItems: Account[] | null = null;
      for (const accountId of targetIds) {
        const data = await updateAccount(accountId, updates);
        latestItems = data.items;
      }
      if (latestItems) {
        applyAccountItems(latestItems);
      }
      setSelectedIds([]);
      toast.success(`${successLabel} ${targetIds.length} 个账号`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "批量更新养号状态失败";
      toast.error(message);
    } finally {
      setIsBulkUpdatingWarming(false);
    }
  };

  const toggleSelectAll = (checked: boolean) => {
    if (checked) {
      setSelectedIds((prev) => Array.from(new Set([...prev, ...currentRows.map((item) => item.id)])));
      return;
    }
    setSelectedIds((prev) => prev.filter((id) => !currentRows.some((row) => row.id === id)));
  };

  const toggleSelectAllFiltered = (checked: boolean) => {
    if (checked) {
      setSelectedIds((prev) => Array.from(new Set([...prev, ...filteredAccounts.map((item) => item.id)])));
      return;
    }
    const filteredIdSet = new Set(filteredAccounts.map((item) => item.id));
    setSelectedIds((prev) => prev.filter((id) => !filteredIdSet.has(id)));
  };

  const selectAllAccounts = () => {
    setSelectedIds(accounts.map((item) => item.id));
  };

  const clearSelection = () => {
    setSelectedIds([]);
  };

  const toggleAccountSelection = (accountId: string, checked: boolean) => {
    setSelectedIds((prev) =>
      checked ? Array.from(new Set([...prev, accountId])) : prev.filter((item) => item !== accountId),
    );
  };

  const renderStatusBadge = (account: Account) => {
    const status = statusMeta[account.status];
    const StatusIcon = status.icon;
    return (
      <Badge variant={status.badge} className="inline-flex items-center gap-1 rounded-md px-2 py-1">
        <StatusIcon className="size-3.5" />
        {account.status}
      </Badge>
    );
  };

  const renderRefreshDiagnostic = (account: Account) => {
    if (!account.lastRefreshError) return null;
    const stage = refreshErrorStageLabels[account.lastRefreshErrorStage ?? ""] ?? account.lastRefreshErrorStage ?? "远端验证";
    return (
      <span
        className="max-w-[16rem] truncate text-xs text-rose-600"
        title={account.lastRefreshError}
      >
        验证失败 · {stage}
      </span>
    );
  };

  const renderWarmingBadge = (account: Account) => {
    if (!account.warmingStatus) return null;
    const errors = account.warmingErrors ?? 0;
    let label = account.warmingStatus === "done" ? `已养熟 D${account.warmingDay ?? 0}` : `养号中 D${account.warmingDay ?? 0}`;
    if (errors >= 3) label += ` (失败${errors}次)`;
    const variant = account.warmingStatus === "done" ? "info" : errors >= 3 ? "danger" : "warning";
    return (
      <div className="flex flex-wrap items-center gap-1">
        <Badge variant={variant as "info" | "warning" | "danger"} className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs">
          {label}
        </Badge>
        {isToday(account.warmingLastActionAt) ? (
          <Badge variant="secondary" className="rounded-md bg-stone-100 px-2 py-1 text-[11px] text-stone-600">
            今日已跑
          </Badge>
        ) : null}
      </div>
    );
  };

  const renderRestoreInfo = (account: Account) => {
    const isRestore = account.status === "限流" || account.status === "过期待刷新" || (!account.imageQuotaUnknown && account.quota <= 0);
    const restore = formatRestoreAt(account.restoreAt, isRestore ? "restore" : "reset");
    return (
      <div className="flex flex-col gap-0.5 text-xs leading-5 text-muted-foreground">
        <span className="font-medium text-foreground">{isRestore ? "恢复时间" : "额度重置"}</span>
        {restore.relative ? <span>{restore.relative}</span> : null}
        <span>{restore.absolute}</span>
      </div>
    );
  };

  const renderTokenLabel = (account: Account) => {
    const secondaryLabel = accountSecondaryLabel(account);
    if (!secondaryLabel) {
      return null;
    }

    return (
      <div className="flex min-w-0 items-center gap-1.5">
        <code className="truncate rounded-md bg-stone-100 px-2 py-1 font-mono text-[11px] font-medium text-muted-foreground">
          {secondaryLabel}
        </code>
        {canExportTokens && account.access_token ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-7 rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => copyToClipboard(account.access_token || "", "token 已复制")}
            aria-label="复制 Token"
            title="复制 Token"
          >
            <Copy className="size-3.5" />
          </Button>
        ) : null}
      </div>
    );
  };

  const renderAccountActions = (account: Account, className?: string) => {
    const rowRefreshing = refreshingAccountIdSet.has(account.id);
    return (
      <div className={cn("flex items-center gap-1 text-muted-foreground", className)}>
        {canUpdateAccount ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8 rounded-lg hover:bg-muted hover:text-foreground"
            onClick={() => openEditDialog(account)}
            disabled={isUpdating}
            aria-label="编辑账号"
            title="编辑账号"
          >
            <Pencil className="size-4" />
          </Button>
        ) : null}
        {canRefreshAccounts ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8 rounded-lg hover:bg-muted hover:text-foreground"
            onClick={() => void handleRefreshAccounts([account.id])}
            disabled={isRefreshing}
            aria-label="刷新账号信息和额度"
            title="刷新账号信息和额度"
          >
            {rowRefreshing ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
          </Button>
        ) : null}
        <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8 rounded-lg hover:bg-muted hover:text-foreground"
            onClick={() => void handleDiagnose(account.id)}
            disabled={isDiagnosing}
            aria-label="诊断账号"
            title="诊断连接状态"
          >
            {isDiagnosing && diagnoseAccountId === account.id ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : (
              <Stethoscope className="size-4" />
            )}
          </Button>
        {canDeleteAccounts ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8 rounded-lg text-rose-500 hover:bg-rose-50 hover:text-rose-600"
            onClick={() => void handleDeleteAccounts([account.id])}
            disabled={isDeleting}
            aria-label="删除账号"
            title="删除账号"
          >
            <Trash2 className="size-4" />
          </Button>
        ) : null}
      </div>
    );
  };

  return (
    <>
      <PageHeader
        eyebrow="Account Pool"
        title="号池管理"
        actions={
          <>
            <Button
              variant="outline"
              className="h-10 rounded-lg"
              onClick={() => void loadAccounts()}
              disabled={isLoading || isRefreshing || isDeleting}
            >
              <RefreshCw className={cn("size-4", isLoading ? "animate-spin" : "")} />
              刷新
            </Button>
            {canRefillReservoir ? (
              <Button
                variant="outline"
                className="h-10 rounded-lg"
                onClick={() => void handleReservoirRefill()}
                disabled={isLoading || isReservoirActionRunning || isDeleting || accounts.length === 0}
              >
                <RefreshCw className={cn("size-4", isReservoirActionRunning ? "animate-spin" : "")} />
                立即补水
              </Button>
            ) : null}
            {canImportAccounts ? (
              <AccountImportDialog
                disabled={isLoading || isRefreshing || isDeleting}
                canImportTokens={canImportTokenAccounts}
                canImportSession={canImportSessionAccounts}
                onImported={(items) => {
                  applyAccountItems(items);
                  setSelectedIds([]);
                  setPage(1);
                }}
              />
            ) : null}
            {canExportTokens ? (
              <Button
                variant="outline"
                className="h-10 rounded-lg"
                onClick={() => void handleExportTokens()}
                disabled={accounts.length === 0 || isExporting}
              >
                {isExporting ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
                导出 Token
              </Button>
            ) : null}
          </>
        }
      />

      <Dialog open={Boolean(editingAccount)} onOpenChange={(open) => (!open ? setEditingAccount(null) : null)}>
        <DialogContent showCloseButton={false} className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>编辑账户</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              手动修改账号状态、类型和额度。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700">状态</label>
              <Select value={editStatus} onValueChange={(value) => setEditStatus(value as AccountStatus)}>
                <SelectTrigger className="h-11 rounded-xl border-stone-200 bg-white">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {accountStatusOptions
                    .filter((option) => option.value !== "all")
                    .map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700">类型</label>
              <Select value={editType} onValueChange={(value) => setEditType(value as AccountType)}>
                <SelectTrigger className="h-11 rounded-xl border-stone-200 bg-white">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {accountTypeOptions
                    .filter((option) => option.value !== "all")
                    .map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700">额度</label>
              <Input
                value={editQuota}
                onChange={(event) => setEditQuota(event.target.value)}
                className="h-11 rounded-xl border-stone-200 bg-white"
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700">养号状态</label>
              <Select value={editWarmingStatus} onValueChange={setEditWarmingStatus}>
                <SelectTrigger className="h-11 rounded-xl border-stone-200 bg-white">
                  <SelectValue placeholder="不养号（正常业务）" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">不养号（正常业务）</SelectItem>
                  <SelectItem value="warming">养号中</SelectItem>
                  <SelectItem value="done">已养熟</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {editWarmingStatus === "warming" ? (
              <div className="space-y-2">
                <label className="text-sm font-medium text-stone-700">已养号天数</label>
                <Input
                  value={editWarmingDay}
                  onChange={(event) => setEditWarmingDay(event.target.value)}
                  type="number"
                  min="0"
                  className="h-11 rounded-xl border-stone-200 bg-white"
                />
              </div>
            ) : null}
          </div>
          <DialogFooter className="pt-2">
            <Button
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => setEditingAccount(null)}
              disabled={isUpdating}
            >
              取消
            </Button>
            <Button
              className="h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800"
              onClick={() => void handleUpdateAccount()}
              disabled={isUpdating || !canUpdateAccount}
            >
              {isUpdating ? <LoaderCircle className="size-4 animate-spin" /> : null}
              保存修改
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Diagnose result dialog */}
      <Dialog
        open={Boolean(diagnoseResult) || (isDiagnosing && Boolean(diagnoseAccountId))}
        onOpenChange={(open) => {
          if (!open) {
            setDiagnoseResult(null);
            setDiagnoseAccountId(null);
          }
        }}
      >
        <DialogContent showCloseButton={false} className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>诊断结果</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              Bootstrap + CheckSession 联合诊断
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {isDiagnosing ? (
              <div className="flex flex-col items-center justify-center gap-3 py-8">
                <LoaderCircle className="size-6 animate-spin text-stone-400" />
                <p className="text-sm text-stone-500">正在诊断中…</p>
              </div>
            ) : diagnoseResult ? (
              <>
                <div className="rounded-xl border border-stone-200 bg-stone-50 p-4">
                  <p className="text-sm font-medium text-stone-700">结论</p>
                  <p className="mt-1 text-sm leading-6 text-stone-600">{diagnoseResult.conclusion}</p>
                </div>

                <div className="grid gap-3 sm:grid-cols-2">
                  <div className={cn(
                    "rounded-xl border p-4",
                    diagnoseResult.bootstrap.ok
                      ? "border-emerald-200 bg-emerald-50"
                      : "border-rose-200 bg-rose-50"
                  )}>
                    <p className="text-sm font-medium text-stone-700">Bootstrap</p>
                    <p className="mt-1 text-sm">
                      {diagnoseResult.bootstrap.ok ? (
                        <span className="text-emerald-700">✓ 成功</span>
                      ) : (
                        <span className="text-rose-600">✗ 失败</span>
                      )}
                    </p>
                    {diagnoseResult.bootstrap.error ? (
                      <p className="mt-1.5 break-all font-mono text-xs leading-5 text-stone-500">
                        {diagnoseResult.bootstrap.error}
                      </p>
                    ) : null}
                  </div>

                  <div className={cn(
                    "rounded-xl border p-4",
                    diagnoseResult.check_session.ok
                      ? "border-emerald-200 bg-emerald-50"
                      : "border-rose-200 bg-rose-50"
                  )}>
                    <p className="text-sm font-medium text-stone-700">CheckSession</p>
                    <p className="mt-1 text-sm">
                      {diagnoseResult.check_session.ok ? (
                        <span className="text-emerald-700">✓ 成功</span>
                      ) : (
                        <span className="text-rose-600">✗ 失败</span>
                      )}
                    </p>
                    {diagnoseResult.check_session.error ? (
                      <p className="mt-1.5 break-all font-mono text-xs leading-5 text-stone-500">
                        {diagnoseResult.check_session.error}
                      </p>
                    ) : null}
                  </div>
                </div>
              </>
            ) : null}
          </div>
          <DialogFooter className="pt-2">
            <Button
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => {
                setDiagnoseResult(null);
                setDiagnoseAccountId(null);
              }}
              disabled={isDiagnosing}
            >
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <section className="mt-5 flex flex-col gap-3">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6">
          {metricCards.map((item) => {
            const Icon = item.icon;
            const value = summary[item.key];
            return (
              <Card key={item.key} className="overflow-hidden rounded-[18px] bg-white/92 shadow-[0_8px_24px_rgba(24,40,72,0.06)]">
                <CardContent className="flex items-center gap-3 p-4">
                  <div className={cn("flex size-10 shrink-0 items-center justify-center rounded-[12px]", item.iconClassName)}>
                    <Icon className="size-4" />
                  </div>
                  <div className="min-w-0">
                    <div className="text-xs font-medium text-muted-foreground">{item.label}</div>
                    <div className="mt-1 flex items-baseline gap-2">
                      <span className="font-display text-2xl leading-none font-semibold text-foreground">
                        {typeof value === "number" ? formatCompact(value) : value}
                      </span>
                    </div>
                    <div className="mt-1 truncate text-xs text-muted-foreground">{item.description}</div>
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      </section>

      {canViewReservoir ? (
        <section className="mt-4">
          <Card className="overflow-hidden rounded-[18px] bg-white/92 shadow-[0_8px_24px_rgba(24,40,72,0.06)]">
            <CardContent className="flex flex-col gap-4 p-4">
              <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                <div className="flex min-w-0 items-center gap-3">
                  <div className="flex size-10 shrink-0 items-center justify-center rounded-[12px] bg-sky-50 text-sky-700 ring-1 ring-sky-100">
                    <RefreshCw className="size-4" />
                  </div>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="text-sm font-semibold text-foreground">蓄水池状态</h3>
                      <Badge variant={reservoirSnapshot?.paused ? "warning" : "secondary"} className="rounded-md px-2 py-1">
                        {reservoirSnapshot?.paused ? "已暂停" : reservoirSnapshot?.mode === "demand_refresh" ? "补水中" : "维护中"}
                      </Badge>
                      {(reservoirSnapshot?.refreshing ?? 0) > 0 ? (
                        <Badge variant="info" className="rounded-md px-2 py-1">刷新中</Badge>
                      ) : null}
                    </div>
                    <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span>水位 {formatCompact(reservoirSnapshot?.currentWater ?? 0)} / {formatCompact(reservoirSnapshot?.targetWater ?? 0)}</span>
                      <span>队列 {formatCompact(reservoirSnapshot?.queueSize ?? 0)}</span>
                      <span>10分钟出水 {formatCompact(reservoirSnapshot?.recentOutflow10m ?? 0)}</span>
                      <span>耗尽 {formatReservoirTime(reservoirSnapshot?.estimatedDepletionAt ?? null)}</span>
                    </div>
                  </div>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  {canRefillReservoir ? (
                    <Button
                      type="button"
                      className="h-9 rounded-lg bg-stone-950 px-3 text-white hover:bg-stone-800"
                      onClick={() => void handleReservoirRefill()}
                      disabled={isReservoirActionRunning || isReservoirLoading}
                    >
                      {isReservoirActionRunning ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                      立即补水
                    </Button>
                  ) : null}
                  {canPauseReservoir || canResumeReservoir ? (
                    <Button
                      type="button"
                      variant="outline"
                      className="h-9 rounded-lg border-stone-200 bg-white px-3"
                      onClick={() => void handleReservoirPauseToggle()}
                      disabled={isReservoirActionRunning || isReservoirLoading}
                    >
                      {reservoirSnapshot?.paused ? <Play className="size-4" /> : <Square className="size-4" />}
                      {reservoirSnapshot?.paused ? "恢复调度" : "暂停调度"}
                    </Button>
                  ) : null}
                  <Button
                    type="button"
                    variant="ghost"
                    className="h-9 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                    onClick={() => void loadReservoir()}
                    disabled={isReservoirLoading}
                  >
                    <RefreshCw className={cn("size-4", isReservoirLoading ? "animate-spin" : "")} />
                    刷新状态
                  </Button>
                </div>
              </div>
              <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
                {Object.entries(reservoirSnapshot?.candidateCounts ?? {}).map(([layer, count]) => (
                  <div key={layer} className="flex items-center justify-between rounded-lg bg-stone-50 px-3 py-2 text-xs">
                    <span className="truncate text-stone-500">{reservoirLayerLabels[layer] ?? layer}</span>
                    <span className="font-semibold text-stone-900">{formatCompact(count)}</span>
                  </div>
                ))}
              </div>
              {reservoirSnapshot?.lastResult ? (
                <div className="rounded-xl border border-stone-200 bg-white p-3">
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                      <div className="text-xs font-semibold text-stone-900">最近一轮调度</div>
                      <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-stone-500">
                        <span>{reservoirSnapshot.lastResult.manual ? "手动触发" : reservoirSnapshot.lastResult.maintenance ? "维护刷新" : "自动补水"}</span>
                        <span>上限 {formatCompact(reservoirSnapshot.lastResult.limit ?? 0)}</span>
                        <span>选中 {formatCompact(reservoirSnapshot.lastResult.selected ?? 0)}</span>
                        <span>成功 {formatCompact(reservoirSnapshot.lastResult.refreshed ?? 0)}</span>
                        <span>失败 {formatCompact(reservoirSnapshot.lastResult.failed ?? 0)}</span>
                        <span>耗时 {formatDurationMs(reservoirSnapshot.lastResult.duration_ms)}</span>
                      </div>
                    </div>
                    <Badge variant={(reservoirSnapshot.lastResult.failed ?? 0) > 0 ? "warning" : "secondary"} className="w-fit rounded-md px-2 py-1">
                      {reservoirSnapshot.lastResult.selected ? "有调度记录" : "未选中账号"}
                    </Badge>
                  </div>
                  {(reservoirSnapshot.lastResult.selected_accounts?.length ?? 0) > 0 ? (
                    <div className="mt-3 grid gap-2 lg:grid-cols-3">
                      {reservoirSnapshot.lastResult.selected_accounts?.map((item, index) => {
                        const detail = reservoirSnapshot.lastResult?.details?.find((entry) => entry.account_id === item.account_id);
                        const error = detail?.error || reservoirSnapshot.lastResult?.errors?.find((entry) => entry.account_id === item.account_id)?.error || "";
                        const statusText = detail?.status === "success" ? "成功" : detail?.status === "pending_session_refresh" ? "待刷新Token" : error ? "失败" : "已选择";
                        const statusClassName = detail?.status === "success"
                          ? "border-emerald-200 bg-emerald-50 text-emerald-700"
                          : error || detail?.status === "error"
                            ? "border-rose-200 bg-rose-50 text-rose-700"
                            : "border-amber-200 bg-amber-50 text-amber-700";
                        return (
                          <div key={`${item.account_id ?? "account"}-${index}`} className="rounded-lg bg-stone-50 px-3 py-2 text-xs">
                            <div className="flex items-center justify-between gap-2">
                              <span className="truncate text-[11px] font-medium text-stone-700">{item.label ?? item.email ?? item.user_id ?? item.token_preview ?? item.account_id ?? "—"}</span>
                              <span className={cn("shrink-0 rounded border px-1.5 py-0.5", statusClassName)}>{statusText}</span>
                            </div>
                            <div className="mt-2 flex items-center justify-between text-stone-500">
                              <span>{reservoirLayerLabels[item.layer ?? ""] ?? item.layer ?? "未知层级"}</span>
                              <span className="font-mono text-[11px]">{item.token_preview ?? item.account_id ?? "—"}</span>
                            </div>
                            <div className="mt-1 flex items-center justify-between text-stone-500">
                              <span>信息年龄</span>
                              <span>{formatDurationSeconds(item.age_seconds)}</span>
                            </div>
                            {detail ? (
                              <div className="mt-1 flex items-center justify-between text-stone-500">
                                <span>{detail.account_status ?? detail.message ?? "结果"}</span>
                                <span>{detail.image_quota_unknown ? "未知额度" : `额度 ${detail.quota ?? "—"}`}</span>
                              </div>
                            ) : null}
                            {error ? <div className="mt-2 break-all text-rose-600">{error}</div> : null}
                          </div>
                        );
                      })}
                    </div>
                  ) : null}
                </div>
              ) : null}
              {reservoirForecastPoints.length > 0 ? (
                <div className="rounded-xl border border-stone-200 bg-stone-50/70 p-3">
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                      <div className="text-xs font-semibold text-stone-900">未来24小时预测</div>
                      <div className="mt-1 text-xs text-stone-500">
                        最低预测水位 {formatCompact(reservoirForecastMinWater ?? 0)}，状态 {reservoirRiskLabel(reservoirForecastRisk ?? undefined)}
                      </div>
                    </div>
                    <Badge className={cn("w-fit rounded-md border px-2 py-1", reservoirRiskClassName(reservoirForecastRisk ?? undefined))}>
                      预测值
                    </Badge>
                  </div>
                  <div className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
                    {reservoirForecastPoints.map((point) => (
                      <div key={point.at} className="rounded-lg bg-white px-3 py-2 text-xs shadow-[0_1px_0_rgba(15,23,42,0.04)]">
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-medium text-stone-700">{formatForecastHour(point.at)}</span>
                          <span className={cn("rounded px-1.5 py-0.5", reservoirRiskClassName(point.riskLevel))}>
                            {reservoirRiskLabel(point.riskLevel)}
                          </span>
                        </div>
                        <div className="mt-2 flex items-center justify-between text-stone-500">
                          <span>水位</span>
                          <span className="font-semibold text-stone-900">{formatCompact(point.estimatedWater)}</span>
                        </div>
                        <div className="mt-1 flex items-center justify-between text-stone-500">
                          <span>回流/出水</span>
                          <span>{formatCompact(point.estimatedInflow)} / {formatCompact(point.estimatedOutflow)}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              ) : null}
              {(reservoirSnapshot?.risks?.length ?? 0) > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {reservoirSnapshot?.risks.map((risk) => (
                    <Badge key={risk} variant="warning" className="rounded-md px-2 py-1">{risk}</Badge>
                  ))}
                </div>
              ) : null}
            </CardContent>
          </Card>
        </section>
      ) : null}
      {canViewWarmingStatus ? (
        <section className="mt-4">
          <Card className="overflow-hidden rounded-[18px] bg-white/92 shadow-[0_8px_24px_rgba(24,40,72,0.06)]">
            <CardContent className="flex flex-col gap-4 p-4 lg:flex-row lg:items-center lg:justify-between">
              <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-[12px] bg-amber-50 text-amber-700 ring-1 ring-amber-100">
                  <Flame className="size-4" />
                </div>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold text-foreground">养号任务</h3>
                    <Badge
                      variant={warmingStatus.running ? "warning" : "secondary"}
                      className="rounded-md px-2 py-1"
                    >
                      {warmingStatus.running ? "运行中" : "未运行"}
                    </Badge>
                    {warmingStatus.last_error ? (
                      <Badge variant="danger" className="rounded-md px-2 py-1">
                        最近错误
                      </Badge>
                    ) : null}
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                    <span>
                      进度 {Math.max(0, warmingStatus.processed)} / {Math.max(0, warmingStatus.total)}
                    </span>
                    <span>当前 {warmingStatus.current_account || "—"}</span>
                    {warmingStatus.last_error ? (
                      <span className="max-w-[32rem] truncate text-rose-600">{warmingStatus.last_error}</span>
                    ) : null}
                  </div>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                {canStartWarming ? (
                  <Button
                    type="button"
                    className="h-9 rounded-lg bg-stone-950 px-3 text-white hover:bg-stone-800"
                    onClick={() => void handleStartWarming()}
                    disabled={warmingStatus.running || isWarmingStarting}
                  >
                    {isWarmingStarting ? <LoaderCircle className="size-4 animate-spin" /> : <Play className="size-4" />}
                    开始养号
                  </Button>
                ) : null}
                {canStopWarming ? (
                  <Button
                    type="button"
                    variant="outline"
                    className="h-9 rounded-lg border-stone-200 bg-white px-3"
                    onClick={() => void handleStopWarming()}
                    disabled={!warmingStatus.running || isWarmingStopping}
                  >
                    {isWarmingStopping ? <LoaderCircle className="size-4 animate-spin" /> : <Square className="size-4" />}
                    停止任务
                  </Button>
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  className="h-9 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                  onClick={() => void loadWarmingStatus()}
                  disabled={isWarmingStatusLoading}
                >
                  <RefreshCw className={cn("size-4", isWarmingStatusLoading ? "animate-spin" : "")} />
                  刷新状态
                </Button>
              </div>
            </CardContent>
          </Card>
        </section>
      ) : null}

      <section className="mt-6 flex flex-col gap-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex items-center gap-3">
            <h2 className="text-lg font-semibold tracking-tight">账户列表</h2>
            <Badge variant="secondary" className="rounded-lg bg-stone-200 px-2 py-0.5 text-stone-700">
              {filteredAccounts.length}
            </Badge>
          </div>

          <div className="grid gap-2 sm:grid-cols-[minmax(16rem,1fr)_10rem_10rem_10rem] lg:min-w-[49rem]">
            <div className="relative min-w-0">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-stone-400" />
              <Input
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value);
                  setPage(1);
                }}
                placeholder="搜索邮箱"
                className="h-10 rounded-lg pl-10"
              />
            </div>
            <Select
              value={typeFilter}
              onValueChange={(value) => {
                setTypeFilter(value as AccountType | "all");
                setPage(1);
              }}
            >
              <SelectTrigger className="h-10 w-full rounded-lg">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {accountTypeOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={statusFilter}
              onValueChange={(value) => {
                setStatusFilter(value as AccountStatus | "all");
                setPage(1);
              }}
            >
              <SelectTrigger className="h-10 w-full rounded-lg">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {accountStatusOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={warmingFilter}
              onValueChange={(value) => {
                setWarmingFilter(value as WarmingFilter);
                setPage(1);
              }}
            >
              <SelectTrigger className="h-10 w-full rounded-lg">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {warmingFilterOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {isLoading && accounts.length === 0 ? (
          <Card>
            <CardContent className="flex flex-col items-center justify-center gap-3 px-6 py-14 text-center">
              <div className="rounded-xl bg-stone-100 p-3 text-stone-500">
                <LoaderCircle className="size-5 animate-spin" />
              </div>
              <div className="space-y-1">
                <p className="text-sm font-medium text-stone-700">正在加载账户</p>
                <p className="text-sm text-stone-500">从后端同步账号列表和状态。</p>
              </div>
            </CardContent>
          </Card>
        ) : null}

        <Card
          className={cn(
            "overflow-hidden",
            isLoading && accounts.length === 0 ? "hidden" : "",
          )}
        >
          <CardContent className="p-0">
            <div className="flex flex-col gap-3 border-b border-stone-100 px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
              <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
                <div className="flex items-center gap-2 rounded-lg bg-stone-100 px-2.5 py-1 text-xs font-medium text-stone-600">
                  <Checkbox
                    checked={currentSelectState}
                    onCheckedChange={(checked) => toggleSelectAll(Boolean(checked))}
                    aria-label="选择当前页账号"
                  />
                  当前页全选
                </div>
                <Button
                  variant="ghost"
                  className="h-8 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                  onClick={() => toggleSelectAllFiltered(!allFilteredSelected)}
                  disabled={filteredAccounts.length === 0}
                >
                  {allFilteredSelected ? "取消筛选全选" : `全选当前筛选 (${filteredAccounts.length})`}
                </Button>
                <Button
                  variant="ghost"
                  className="h-8 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                  onClick={() => (allAccountsSelected ? clearSelection() : selectAllAccounts())}
                  disabled={accounts.length === 0}
                >
                  {allAccountsSelected ? "取消全选" : `全选全部 (${accounts.length})`}
                </Button>
                {selectedIds.length > 0 ? (
                  <Button
                    variant="ghost"
                    className="h-8 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                    onClick={clearSelection}
                  >
                    清空选择
                  </Button>
                ) : null}
                {canRefreshAccounts ? (
                  <Button
                    variant="ghost"
                    className="h-8 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                    onClick={() => void handleRefreshAccounts(selectedAccountIds)}
                    disabled={selectedAccountIds.length === 0 || isRefreshing}
                  >
                    {isRefreshing ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                    刷新选中
                  </Button>
                ) : null}
                {canUpdateAccount ? (
                  <>
                    <Button
                      variant="ghost"
                      className="h-8 rounded-lg px-3 text-amber-700 hover:bg-amber-50 hover:text-amber-800"
                      onClick={() =>
                        void handleBulkUpdateWarming(
                          selectedFilteredAccountIds,
                          { warming_status: "warming", warming_day: 0 },
                          "已设为养号中",
                        )
                      }
                      disabled={selectedFilteredAccountIds.length === 0 || isBulkUpdatingWarming}
                    >
                      {isBulkUpdatingWarming ? <LoaderCircle className="size-4 animate-spin" /> : <Flame className="size-4" />}
                      设为养号中
                    </Button>
                    <Button
                      variant="ghost"
                      className="h-8 rounded-lg px-3 text-sky-700 hover:bg-sky-50 hover:text-sky-800"
                      onClick={() =>
                        void handleBulkUpdateWarming(
                          selectedFilteredAccountIds,
                          { warming_status: "done" },
                          "已设为已养熟",
                        )
                      }
                      disabled={selectedFilteredAccountIds.length === 0 || isBulkUpdatingWarming}
                    >
                      {isBulkUpdatingWarming ? <LoaderCircle className="size-4 animate-spin" /> : <CheckCircle2 className="size-4" />}
                      设为已养熟
                    </Button>
                    <Button
                      variant="ghost"
                      className="h-8 rounded-lg px-3 text-stone-600 hover:bg-stone-100"
                      onClick={() =>
                        void handleBulkUpdateWarming(
                          selectedFilteredAccountIds,
                          { warming_status: null },
                          "已取消养号",
                        )
                      }
                      disabled={selectedFilteredAccountIds.length === 0 || isBulkUpdatingWarming}
                    >
                      {isBulkUpdatingWarming ? <LoaderCircle className="size-4 animate-spin" /> : <CircleOff className="size-4" />}
                      取消养号
                    </Button>
                  </>
                ) : null}
                {canDeleteAccounts ? (
                  <>
                    <Button
                      variant="ghost"
                      className="h-8 rounded-lg px-3 text-rose-500 hover:bg-rose-50 hover:text-rose-600"
                      onClick={() => void handleDeleteAccounts(abnormalAccountIds)}
                      disabled={abnormalAccountIds.length === 0 || isDeleting}
                    >
                      {isDeleting ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                      移除异常账号
                    </Button>
                    <Button
                      variant="ghost"
                      className="h-8 rounded-lg px-3 text-rose-500 hover:bg-rose-50 hover:text-rose-600"
                      onClick={() => void handleDeleteAccounts(selectedAccountIds)}
                      disabled={selectedAccountIds.length === 0 || isDeleting}
                    >
                      {isDeleting ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                      删除所选{selectedAccountIds.length > 0 ? ` (${selectedAccountIds.length})` : ""}
                    </Button>
                  </>
                ) : null}
                {selectedIds.length > 0 ? (
                  <span className="rounded-lg bg-[#edf4ff] px-2.5 py-1 text-xs font-medium text-[#1456f0]">
                    已选择 {selectedIds.length} 项，当前筛选 {selectedFilteredAccountIds.length} 项
                  </span>
                ) : null}
              </div>
            </div>

            {showInitialEmptyState ? (
              <div className="flex min-h-[260px] flex-col items-center justify-center gap-3 px-6 py-14 text-center">
                <div className="rounded-[16px] bg-[#edf4ff] p-4 text-[#1456f0] ring-1 ring-blue-100">
                  <UserRound className="size-7" />
                </div>
                <div className="space-y-1">
                  <p className="text-sm font-medium text-foreground">暂无账号</p>
                  <p className="max-w-[28rem] text-sm leading-6 text-muted-foreground">
                    导入 Token 后，账号状态、类型、额度和调用统计会在这里显示。
                  </p>
                </div>
              </div>
            ) : (
              <>
                <div className="hidden overflow-x-auto md:block">
                  <Table className="min-w-[940px]">
                    <TableHeader>
                      <TableRow>
                        <TableHead className="w-12">
                          <Checkbox
                            checked={currentSelectState}
                            onCheckedChange={(checked) => toggleSelectAll(Boolean(checked))}
                            aria-label="选择当前页账号"
                          />
                        </TableHead>
                        <TableHead className="w-[34%]">账号</TableHead>
                        <TableHead className="w-48">状态 / 类型</TableHead>
                        <TableHead className="w-28">养号</TableHead>
                        <TableHead className="w-32">额度</TableHead>
                        <TableHead className="w-44">重置/恢复时间</TableHead>
                        <TableHead className="w-36">调用</TableHead>
                        <TableHead className="w-28 text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {currentRows.map((account) => (
                        <TableRow key={account.id} className="text-sm text-muted-foreground">
                          <TableCell>
                            <Checkbox
                              checked={selectedIds.includes(account.id)}
                              onCheckedChange={(checked) => toggleAccountSelection(account.id, Boolean(checked))}
                              aria-label="选择账号"
                            />
                          </TableCell>
                          <TableCell>
                            <div className="flex min-w-0 flex-col gap-1.5">
                              <span className="truncate font-medium tracking-tight text-foreground">
                                {accountPrimaryLabel(account)}
                              </span>
                              {renderTokenLabel(account)}
                            </div>
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-col items-start gap-1.5">
                              <div className="flex flex-wrap items-center gap-1.5">
                                {renderStatusBadge(account)}
                                <Badge variant="secondary" className="rounded-md px-2 py-1">
                                  {account.type}
                                </Badge>
                              </div>
                              {renderRefreshDiagnostic(account)}
                            </div>
                          </TableCell>
                          <TableCell>
                            {renderWarmingBadge(account)}
                          </TableCell>
                          <TableCell>
                            <Badge variant="info" className="rounded-md px-2 py-1">
                              {formatQuota(account)}
                            </Badge>
                          </TableCell>
                          <TableCell>{renderRestoreInfo(account)}</TableCell>
                          <TableCell>
                            <div className="flex flex-col gap-1 text-xs leading-5">
                              <span className="text-emerald-700">成功 {account.success}</span>
                              <span className="text-rose-600">失败 {account.fail}</span>
                            </div>
                          </TableCell>
                          <TableCell>
                            {renderAccountActions(account, "justify-end")}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>

                {currentRows.length > 0 ? (
                  <div className="flex flex-col gap-3 p-3 md:hidden">
                    {currentRows.map((account) => (
                      <div key={account.id} className="rounded-[14px] border border-stone-100 bg-white p-3 shadow-[0_4px_14px_rgba(24,40,72,0.05)]">
                        <div className="flex items-start gap-3">
                          <Checkbox
                            checked={selectedIds.includes(account.id)}
                            onCheckedChange={(checked) => toggleAccountSelection(account.id, Boolean(checked))}
                            className="mt-1"
                            aria-label="选择账号"
                          />
                          <div className="min-w-0 flex-1">
                            <div className="flex min-w-0 items-start justify-between gap-2">
                              <div className="min-w-0">
                                <div className="truncate text-sm font-semibold text-foreground">
                                  {accountPrimaryLabel(account)}
                                </div>
                                <div className="mt-1">{renderTokenLabel(account)}</div>
                              </div>
                              {renderAccountActions(account, "shrink-0")}
                            </div>

                            <div className="mt-3 flex flex-wrap items-center gap-1.5">
                              {renderStatusBadge(account)}
                              <Badge variant="secondary" className="rounded-md px-2 py-1">
                                {account.type}
                              </Badge>
                              {renderWarmingBadge(account)}
                              <Badge variant="info" className="rounded-md px-2 py-1">
                                额度 {formatQuota(account)}
                              </Badge>
                            </div>
                            {renderRefreshDiagnostic(account) ? (
                              <div className="mt-2">{renderRefreshDiagnostic(account)}</div>
                            ) : null}

                            <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
                              <div className="rounded-lg bg-stone-50 p-2">
                                <div className="text-muted-foreground">调用</div>
                                <div className="mt-1 font-medium text-foreground">
                                  成功 {account.success} / 失败 {account.fail}
                                </div>
                              </div>
                              <div className="rounded-lg bg-stone-50 p-2">
                                <div className="text-muted-foreground">恢复</div>
                                <div className="mt-1">{renderRestoreInfo(account)}</div>
                              </div>
                            </div>
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                ) : null}

                {showFilteredEmptyState ? (
                  <div className="flex flex-col items-center justify-center gap-3 px-6 py-14 text-center">
                    <div className="rounded-xl bg-stone-100 p-3 text-stone-500">
                      <Search className="size-5" />
                    </div>
                    <div className="flex flex-col gap-1">
                      <p className="text-sm font-medium text-stone-700">没有匹配的账户</p>
                      <p className="text-sm text-stone-500">调整筛选条件或搜索关键字后重试。</p>
                    </div>
                  </div>
                ) : null}

            <div className="border-t border-stone-100 px-4 py-4">
              <div className="flex items-center justify-center gap-3 overflow-x-auto whitespace-nowrap">
                <div className="shrink-0 text-sm text-stone-500">
                显示第 {filteredAccounts.length === 0 ? 0 : startIndex + 1} -{" "}
                {Math.min(startIndex + Number(pageSize), filteredAccounts.length)} 条，共{" "}
                {filteredAccounts.length} 条
                </div>

                <span className="shrink-0 text-sm leading-none text-stone-500">
                  {safePage} / {pageCount} 页
                </span>
                <Select
                  value={pageSize}
                  onValueChange={(value) => {
                    setPageSize(value);
                    setPage(1);
                  }}
                >
                  <SelectTrigger className="h-10 w-[108px] shrink-0 rounded-lg border-stone-200 bg-white text-sm leading-none">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="10">10 / 页</SelectItem>
                    <SelectItem value="20">20 / 页</SelectItem>
                    <SelectItem value="50">50 / 页</SelectItem>
                    <SelectItem value="100">100 / 页</SelectItem>
                  </SelectContent>
                </Select>
                <Button
                  variant="outline"
                  size="icon"
                  className="size-10 shrink-0 rounded-lg border-stone-200 bg-white"
                  disabled={safePage <= 1}
                  onClick={() => setPage((prev) => Math.max(1, prev - 1))}
                >
                  <ChevronLeft className="size-4" />
                </Button>
                {paginationItems.map((item, index) =>
                  item === "..." ? (
                    <span key={`ellipsis-${index}`} className="px-1 text-sm text-stone-400">
                      ...
                    </span>
                  ) : (
                    <Button
                      key={item}
                      variant={item === safePage ? "default" : "outline"}
                      className={cn(
                        "h-10 min-w-10 shrink-0 rounded-lg px-3",
                        item === safePage
                          ? "bg-stone-950 text-white hover:bg-stone-800"
                          : "border-stone-200 bg-white text-stone-700",
                      )}
                      onClick={() => setPage(item)}
                    >
                      {item}
                    </Button>
                  ),
                )}
                <Button
                  variant="outline"
                  size="icon"
                  className="size-10 shrink-0 rounded-lg border-stone-200 bg-white"
                  disabled={safePage >= pageCount}
                  onClick={() => setPage((prev) => Math.min(pageCount, prev + 1))}
                >
                  <ChevronRight className="size-4" />
                </Button>
              </div>
            </div>
              </>
            )}
          </CardContent>
        </Card>
      </section>
    </>
  );
}

export default function AccountsPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/accounts");

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  return <AccountsPageContent session={session} />;
}

