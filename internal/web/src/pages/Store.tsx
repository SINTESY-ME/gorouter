import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Card, Chip, Input, Spinner, AlertDialog } from "@heroui/react";
import { api, type StoreEntry } from "../api";

export default function Store() {
  const { t } = useTranslation();
  const [items, setItems] = useState<StoreEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmId, setConfirmId] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    setError("");
    api.providers.store.list()
      .then(setItems)
      .catch((e) => setError(e?.message ?? t("store.errorLoad")))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const install = async (id: string) => {
    setBusy(id);
    try {
      await api.providers.store.install(id);
      load();
    } catch (e: any) {
      setError(e?.message ?? t("store.errorInstall"));
    } finally {
      setBusy(null);
    }
  };

  const remove = async (id: string) => {
    setBusy(id);
    try {
      await api.providers.store.remove(id);
      load();
    } catch (e: any) {
      setError(e?.message ?? t("store.errorRemove"));
    } finally {
      setBusy(null);
    }
  };

  const q = query.trim().toLowerCase();
  const filtered = !q
    ? items
    : items.filter((i) =>
        i.id.toLowerCase().includes(q) ||
        i.name.toLowerCase().includes(q) ||
        i.category?.toLowerCase().includes(q) ||
        i.capabilities?.some((c) => c.toLowerCase().includes(q))
      );

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-end gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("store.title")}</h1>
          <p className="text-sm text-muted mt-0.5">
            {t("store.subtitle")}
          </p>
        </div>
        <div className="flex gap-2">
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("store.searchPlaceholder")}
            className="max-w-xs"
            variant="secondary"
            aria-label={t("store.searchAria")}
          />
          <Button size="sm" variant="secondary" onPress={load}>{t("store.refresh")}</Button>
        </div>
      </div>

      {error && (
        <div className="bg-danger-soft border border-danger/30 text-danger rounded-xl p-4 text-sm">{error}</div>
      )}

      {loading ? (
        <div className="flex justify-center py-20"><Spinner /></div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-20 text-muted text-sm">
          {t("store.empty")}
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {filtered.map((e) => (
            <Card key={e.id} className="p-4 flex flex-col gap-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="w-2.5 h-2.5 rounded-full shrink-0 bg-muted" style={e.color ? { background: e.color } : undefined} />
                    <h3 className="font-semibold truncate">{e.name || e.id}</h3>
                  </div>
                  <p className="text-xs text-muted font-mono mt-0.5">{e.id}</p>
                </div>
                <Chip size="sm" variant="soft" color={e.installed ? "success" : "default"}>
                  {e.installed ? t("store.installed") : t("store.available")}
                </Chip>
              </div>
              {e.category && (
                <Chip size="sm" variant="soft">{e.category}</Chip>
              )}
              {e.capabilities && e.capabilities.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {e.capabilities.map((c) => (
                    <Chip key={c} size="sm" variant="soft" className="h-5 text-[10px]">{c}</Chip>
                  ))}
                </div>
              )}
              <div className="mt-auto pt-1">
                {e.installed ? (
                  <Button size="sm" variant="danger-soft" className="w-full" isDisabled={busy === e.id} onPress={() => setConfirmId(e.id)}>
                    {t("store.remove")}
                  </Button>
                ) : (
                  <Button size="sm" variant="outline" className="w-full" isDisabled={busy === e.id} onPress={() => install(e.id)}>
                    {t("store.install")}
                  </Button>
                )}
              </div>
            </Card>
          ))}
        </div>
      )}

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={!!confirmId} onOpenChange={(o) => !o && setConfirmId(null)}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[400px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t("store.removeTitle", { id: confirmId })}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("store.removeBody")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("store.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmId) remove(confirmId); setConfirmId(null); }}>{t("store.remove")}</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
