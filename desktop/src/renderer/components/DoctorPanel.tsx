import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, LoaderCircle, MessageCircleQuestion, RefreshCw, TriangleAlert, Wrench } from "lucide-react";
import { toast } from "sonner";
import * as T from "../../shared/api";
import { api } from "../lib/api";
import { canAskHome, isCommand, openHomeChat, problems } from "../lib/doctor";
import { useT } from "../lib/i18n";
import { chooseSession } from "../lib/sessions";
import { cn, errorMessage } from "../lib/utils";
import { CommandBox } from "./SettingsView";
import { Button } from "./ui/button";
import { Notice, Panel } from "./ui/card";

// DoctorPanel is Setup's list of what's wrong (`agentbox doctor`): each check
// that isn't ok, with what it found and what fixes it. What fixes itself has
// a Fix button; a failure with no fix of its own can be handed to the Home
// chat, which opens a session told what failed. The checks reach the network,
// so they run again every half minute, or when asked, not every few seconds.
// With compact, as in the wizard, it shows nothing while everything passes.
export function DoctorPanel({ compact }: { compact?: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const doctor = useQuery({ queryKey: ["doctor"], queryFn: api.doctor, refetchInterval: 30_000 });
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["doctor"] });
  const fix = useMutation({
    mutationFn: (checks?: string[]) => api.doctorFix({ checks }),
    onSuccess: (res) => {
      queryClient.setQueryData(["doctor"], res.report);
      void queryClient.invalidateQueries({ queryKey: ["setup"] });
      for (const f of res.fixes) if (f.error) toast.error(t("doctor.fixFailed", { check: f.id }), { description: f.error });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const askHome = useMutation({
    mutationFn: (check: string) => api.doctorHome({ check }),
    onSuccess: (res) => {
      chooseSession(T.HomeProject, res.session);
      void queryClient.invalidateQueries({ queryKey: ["leadSessions", T.HomeProject] });
      openHomeChat();
    },
    onError: (err) => toast.error(t("doctor.askHomeFailed"), { description: errorMessage(err) }),
  });

  if (doctor.error) return compact ? null : <Notice>{errorMessage(doctor.error)}</Notice>;
  if (!doctor.data)
    return compact ? null : (
      <div className="flex items-center gap-2 text-ui text-subtle">
        <LoaderCircle className="size-4 animate-spin" /> {t("doctor.checking")}
      </div>
    );
  const list = problems(doctor.data.checks);
  if (list.length === 0 && compact) return null;
  const fixable = list.filter((c) => c.autoFix && !c.job).map((c) => c.id);

  return (
    <div className="grid gap-3" data-doctor>
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="min-w-0 flex-1 text-body font-semibold text-primary">
          {list.length === 0 ? t("doctor.allOk") : t("doctor.title", { count: list.length })}
        </h3>
        {fixable.length > 1 && (
          <Button size="sm" onClick={() => fix.mutate(undefined)} disabled={fix.isPending}>
            <Wrench /> {t("doctor.fixAll", { count: fixable.length })}
          </Button>
        )}
        <Button size="sm" variant="ghost" onClick={refresh} disabled={doctor.isFetching}>
          <RefreshCw className={cn(doctor.isFetching && "animate-spin")} /> {t("doctor.checkAgain")}
        </Button>
      </div>
      {list.length === 0 && (
        <p className="flex items-center gap-2 text-ui text-muted">
          <CircleCheck className="size-4 text-emerald-400" /> {t("doctor.allOkDetail", { count: doctor.data.checks.length })}
        </p>
      )}
      {list.map((c) => (
        <Panel key={c.id} className={cn("p-4", c.status === T.DoctorFail && "border-red-400/20")} data-doctor-check={c.id} data-status={c.status}>
          <div className="flex items-start gap-3">
            {c.job ? (
              <LoaderCircle className="mt-0.5 size-5 shrink-0 animate-spin text-sky-400" />
            ) : c.status === T.DoctorFail ? (
              <CircleX className="mt-0.5 size-5 shrink-0 text-red-400" />
            ) : (
              <TriangleAlert className="mt-0.5 size-5 shrink-0 text-amber-300" />
            )}
            <div className="grid min-w-0 flex-1 gap-2">
              <div>
                <p className="text-body font-semibold text-primary">{c.title}</p>
                <p className="break-words text-ui text-muted">{c.message}</p>
              </div>
              {c.job ? (
                <p className="text-xs text-subtle">{t("doctor.beingFixed")}</p>
              ) : (
                c.fix &&
                !c.autoFix &&
                (isCommand(c.fix) ? (
                  <CommandBox command={c.fix} />
                ) : (
                  <p className="break-words text-ui text-secondary">{t("doctor.fix", { fix: c.fix })}</p>
                ))
              )}
              {c.id === T.DoctorAILogin && c.status === T.DoctorFail && (
                <p className="text-xs text-subtle">{t("doctor.loginFirst")}</p>
              )}
              {(c.autoFix || canAskHome(c)) && !c.job && (
                <div className="flex flex-wrap gap-2">
                  {c.autoFix && (
                    <Button size="sm" onClick={() => fix.mutate([c.id])} disabled={fix.isPending}>
                      <Wrench /> {t("doctor.fixIt")}
                    </Button>
                  )}
                  {canAskHome(c) && (
                    <Button size="sm" variant="outline" onClick={() => askHome.mutate(c.id)} disabled={askHome.isPending}>
                      {askHome.isPending && askHome.variables === c.id ? <LoaderCircle className="animate-spin" /> : <MessageCircleQuestion />}
                      {t("doctor.askHome")}
                    </Button>
                  )}
                </div>
              )}
            </div>
          </div>
        </Panel>
      ))}
    </div>
  );
}
