import { useEffect, useState, type FormEvent } from "react";
import { FolderGit2, GitBranch, Pencil, Plus, ShieldAlert } from "lucide-react";
import { Link } from "react-router-dom";
import { errorMessage } from "@/api/client";
import {
  useAgents,
  useCreateProject,
  useHermesModels,
  useHermesProfiles,
  usePatchProject,
  useProjects,
  useTestProjectTelegram,
} from "@/api/queries";
import {
  AGENT_ROLES,
  type AgentRole,
  type HermesProfile,
  type ModelChoice,
  type ModelProvider,
  type IntegrationMode,
  type Project,
  type ProjectMode,
} from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Field, Input, Mono, Select } from "@/components/ui/primitives";
import { Tag, ToneBadge } from "@/components/ui/StateBadge";
import { Switch } from "@/components/ui/Switch";
import { EmptyState, QueryView } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { INTEGRATION_HINTS, roleLabel } from "@/lib/states";
import {
  LIMITS,
  validBranch,
  validCheckName,
  validProfile,
  validProjectName,
  validRepository,
  validSlug,
  validTelegramChat,
  validTestCommand,
} from "@/lib/validation";

interface FormState {
  slug: string;
  name: string;
  repository: string;
  base_branch: string;
  required_checks: string;
  mode: ProjectMode;
  test_command: string;
  profile_developer: string;
  profile_reviewer: string;
  profile_qa: string;
  /** Rôle → "provider/model" imposé ("" = défaut du profil). */
  models: Partial<Record<AgentRole, string>>;
  telegram_chat: string;
  telegram_thread: string;
  integration_mode: IntegrationMode;
}

/** Valeur d'option du sélecteur de modèle : "<provider>/<model>" (le modèle peut contenir des « / »). */
export function modelKey(m: ModelChoice | undefined): string {
  return m && m.model ? `${m.provider}/${m.model}` : "";
}
export function parseModelKey(key: string): ModelChoice | undefined {
  const i = key.indexOf("/");
  if (i <= 0 || i === key.length - 1) return undefined;
  return { provider: key.slice(0, i), model: key.slice(i + 1) };
}

// Rôles qui exécutent du code : ils peuvent avoir un profil Hermes propre au projet (image Docker
// outillée, seuls les worktrees du projet montés). PO et Tech Lead restent communs.
const PROFILE_ROLES = [
  { role: "developer", key: "profile_developer", label: "Profil développeur" },
  { role: "reviewer", key: "profile_reviewer", label: "Profil reviewer" },
  { role: "qa", key: "profile_qa", label: "Profil QA" },
] as const;

const EMPTY: FormState = {
  slug: "",
  name: "",
  repository: "",
  base_branch: "main",
  required_checks: "",
  mode: "sandbox",
  test_command: "",
  profile_developer: "",
  profile_reviewer: "",
  profile_qa: "",
  models: {},
  telegram_chat: "",
  telegram_thread: "",
  integration_mode: "direct",
};

/** Lien vers le groupe/sujet Telegram (supergroupes : -100… → t.me/c/…). */
export function telegramLink(chat: string, thread: string): string | null {
  const m = /^-100(\d+)$/.exec(chat.trim());
  if (!m) return null;
  const t = thread.trim();
  return /^[1-9]\d*$/.test(t)
    ? `https://t.me/c/${m[1]}/${t}`
    : `https://t.me/c/${m[1]}`;
}

function agentProfiles(f: FormState): Partial<Record<AgentRole, string>> {
  const out: Partial<Record<AgentRole, string>> = {};
  for (const { role, key } of PROFILE_ROLES)
    if (f[key].trim()) out[role] = f[key].trim();
  return out;
}

function agentModels(f: FormState): Partial<Record<AgentRole, ModelChoice>> {
  const out: Partial<Record<AgentRole, ModelChoice>> = {};
  for (const role of AGENT_ROLES) {
    const m = parseModelKey(f.models[role] ?? "");
    if (m) out[role] = m;
  }
  return out;
}

function modelsOf(p: Project | null): Partial<Record<AgentRole, string>> {
  const out: Partial<Record<AgentRole, string>> = {};
  for (const role of AGENT_ROLES) {
    const k = modelKey(p?.agent_models?.[role]);
    if (k) out[role] = k;
  }
  return out;
}

export function validateProject(
  f: FormState,
  isCreate: boolean,
): Partial<Record<keyof FormState, string>> {
  const e: Partial<Record<keyof FormState, string>> = {};
  if (isCreate && !validSlug(f.slug))
    e.slug =
      "1 à 40 caractères : minuscules, chiffres, tirets (pas de tiret en tête).";
  if (!validProjectName(f.name))
    e.name = f.name.trim()
      ? `${LIMITS.projectName} caractères maximum.`
      : "Le nom est requis.";
  if (!validRepository(f.repository.trim()))
    e.repository = "Format attendu : owner/repo.";
  if (!validBranch(f.base_branch.trim()))
    e.base_branch = f.base_branch.trim()
      ? "Nom de branche invalide."
      : "La branche de base est requise.";
  if (!validTestCommand(f.test_command))
    e.test_command = `${LIMITS.testCommand} caractères maximum.`;
  for (const { key } of PROFILE_ROLES) {
    const v = f[key].trim();
    if (v && !validProfile(v))
      e[key] = "Minuscules, chiffres, - et _ (64 caractères maximum).";
  }
  const checks = parseChecks(f.required_checks);
  if (checks.length > LIMITS.requiredChecks)
    e.required_checks = `${LIMITS.requiredChecks} checks maximum.`;
  else if (checks.some((c) => !validCheckName(c)))
    e.required_checks = `Nom de check invalide (${LIMITS.checkName} caractères maximum, sans tabulation).`;
  const chat = f.telegram_chat.trim();
  const thread = f.telegram_thread.trim();
  if (!validTelegramChat(chat))
    e.telegram_chat = "Identifiant de groupe numérique (ex. -1001234567890).";
  if (thread && !/^[1-9]\d{0,9}$/.test(thread))
    e.telegram_thread = "Numéro de sujet (entier positif).";
  else if (thread && !chat) e.telegram_thread = "Un sujet exige un groupe.";
  return e;
}

function parseChecks(s: string): string[] {
  return s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

// Liste des profils d'un rôle : défaut de l'agent, profils du rôle, puis les autres profils.
// Une valeur enregistrée dont la clé a disparu reste visible (signalée) plutôt que d'être effacée.
export function ProfileSelect({
  role,
  value,
  onChange,
  defaultProfile,
  profiles,
  ...rest
}: {
  role: AgentRole;
  value: string;
  onChange: (v: string) => void;
  defaultProfile?: string;
  profiles: HermesProfile[];
  id?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  const own = profiles.filter(
    (p) => p.role === role && p.profile !== defaultProfile,
  );
  const others = profiles.filter(
    (p) => p.role !== role && p.profile !== defaultProfile,
  );
  const known = value === "" || profiles.some((p) => p.profile === value);
  return (
    <Select
      {...rest}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="font-mono"
    >
      <option value="">
        {defaultProfile ? `Par défaut (${defaultProfile})` : "Par défaut"}
      </option>
      {!known && <option value={value}>{value} — clé absente</option>}
      {own.map((p) => (
        <option key={p.profile} value={p.profile}>
          {p.profile}
        </option>
      ))}
      {others.length > 0 && (
        <optgroup label="Autres profils">
          {others.map((p) => (
            <option key={p.profile} value={p.profile}>
              {p.profile}
            </option>
          ))}
        </optgroup>
      )}
    </Select>
  );
}

// Liste des modèles d'un rôle, groupés par fournisseur authentifié. Première option : défaut du
// profil. Une valeur enregistrée absente du catalogue reste visible (signalée) plutôt que d'être effacée.
export function ModelSelect({
  value,
  onChange,
  providers,
  emptyLabel = "Défaut du profil",
  ...rest
}: {
  value: string;
  onChange: (v: string) => void;
  providers: ModelProvider[];
  emptyLabel?: string;
  id?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  const known =
    value === "" ||
    providers.some((p) => p.models.some((m) => `${p.provider}/${m}` === value));
  return (
    <Select
      {...rest}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="font-mono"
    >
      <option value="">{emptyLabel}</option>
      {!known && <option value={value}>{value} (absent)</option>}
      {providers.map((p) => (
        <optgroup
          key={p.provider}
          label={p.is_current ? `${p.name} (courant)` : p.name}
        >
          {p.models.map((m) => (
            <option key={m} value={`${p.provider}/${m}`}>
              {m}
            </option>
          ))}
        </optgroup>
      ))}
    </Select>
  );
}

// Bloc « Modèles par agent » : un sélecteur par rôle, catalogue chargé depuis Hermes. Si le
// catalogue est indisponible, le formulaire reste utilisable (sélections existantes visibles).
export function AgentModelsFields({
  values,
  onChange,
  providers,
  error,
  loading,
}: {
  values: Partial<Record<AgentRole, string>>;
  onChange: (role: AgentRole, v: string) => void;
  providers: ModelProvider[];
  error?: string;
  loading?: boolean;
}) {
  return (
    <fieldset className="grid gap-4 sm:col-span-2 sm:grid-cols-2">
      <legend className="mb-1 text-sm font-medium text-fg sm:col-span-2">
        Modèles par agent
      </legend>
      <p className="-mt-2 text-xs text-fg-subtle sm:col-span-2">
        Fournisseur et modèle imposés aux runs de ce projet. Sans choix : modèle
        souhaité de l&apos;agent, sinon défaut du profil Hermes. Si le
        fournisseur est refusé par Hermes, le run est re-soumis une fois sur le
        défaut du profil (signalé sur le run).
      </p>
      {error && (
        <p role="alert" className="text-sm text-warning sm:col-span-2">
          Catalogue des modèles indisponible : {error}. Les choix enregistrés
          restent visibles ; aucun nouveau modèle ne peut être sélectionné.
        </p>
      )}
      {AGENT_ROLES.map((role) => (
        <Field
          key={role}
          label={`Modèle ${roleLabel(role)}`}
          hint={loading ? "Chargement du catalogue…" : undefined}
        >
          {(p) => (
            <ModelSelect
              {...p}
              value={values[role] ?? ""}
              onChange={(v) => onChange(role, v)}
              providers={providers}
            />
          )}
        </Field>
      ))}
    </fieldset>
  );
}

// Lien vers le canal et envoi d'un message de test (configuration enregistrée uniquement).
function TelegramTest({
  project,
  chat,
  thread,
}: {
  project: Project | null;
  chat: string;
  thread: string;
}) {
  const test = useTestProjectTelegram();
  const toast = useToast();
  const link = telegramLink(chat, thread);
  const dirty =
    !!project &&
    (chat.trim() !== (project.telegram_chat_id ?? "") ||
      thread.trim() !==
        (project.telegram_thread_id ? String(project.telegram_thread_id) : ""));
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm sm:col-span-2">
      {link && (
        <a
          href={link}
          target="_blank"
          rel="noreferrer"
          className="text-accent underline"
        >
          Ouvrir dans Telegram
        </a>
      )}
      {project && (
        <Button
          type="button"
          variant="ghost"
          loading={test.isPending}
          disabled={dirty}
          title={dirty ? "Enregistre d'abord le canal" : undefined}
          onClick={() =>
            test.mutate(project.id, {
              onSuccess: () => toast.success("Message de test envoyé"),
              onError: (e) => toast.error(errorMessage(e)),
            })
          }
        >
          Envoyer un message de test
        </Button>
      )}
      {dirty && (
        <span className="text-muted">
          Enregistre d&apos;abord pour tester ce canal.
        </span>
      )}
    </div>
  );
}

function ProjectDialog({
  open,
  project,
  onClose,
}: {
  open: boolean;
  project: Project | null;
  onClose: () => void;
}) {
  const isCreate = !project;
  const [f, setF] = useState<FormState>(EMPTY);
  const [touched, setTouched] = useState(false);
  const create = useCreateProject();
  const patch = usePatchProject();
  const toast = useToast();
  const m = isCreate ? create : patch;

  useEffect(() => {
    if (!open) return;
    setTouched(false);
    setF(
      project
        ? {
            slug: project.slug,
            name: project.name,
            repository: project.repository,
            base_branch: project.base_branch,
            required_checks: project.required_checks.join("\n"),
            mode: project.mode,
            test_command: project.test_command ?? "",
            profile_developer: project.agent_profiles?.developer ?? "",
            profile_reviewer: project.agent_profiles?.reviewer ?? "",
            profile_qa: project.agent_profiles?.qa ?? "",
            models: modelsOf(project),
            telegram_chat: project.telegram_chat_id ?? "",
            telegram_thread: project.telegram_thread_id
              ? String(project.telegram_thread_id)
              : "",
            integration_mode: project.integration_mode ?? "direct",
          }
        : EMPTY,
    );
  }, [open, project]);

  const profiles = useHermesProfiles(open);
  const models = useHermesModels(open);
  const agents = useAgents();
  const defaults = Object.fromEntries(
    (agents.data ?? []).map((a) => [a.role, a.hermes_profile]),
  ) as Partial<Record<AgentRole, string>>;
  const errors = validateProject(f, isCreate);
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) =>
    setF((s) => ({ ...s, [k]: v }));
  const err = (k: keyof FormState) => (touched ? errors[k] : undefined);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (Object.keys(errors).length) return;
    const body = {
      name: f.name.trim(),
      repository: f.repository.trim(),
      base_branch: f.base_branch.trim(),
      required_checks: parseChecks(f.required_checks),
      mode: f.mode,
      test_command: f.test_command.trim(),
      agent_profiles: agentProfiles(f),
      agent_models: agentModels(f),
      telegram_chat_id: f.telegram_chat.trim(),
      telegram_thread_id: f.telegram_thread.trim()
        ? Number(f.telegram_thread.trim())
        : 0,
      integration_mode: f.integration_mode,
    };
    const done = {
      onSuccess: () => {
        toast.success(isCreate ? "Projet créé" : "Projet mis à jour");
        onClose();
      },
    };
    if (isCreate) create.mutate({ slug: f.slug, ...body }, done);
    else patch.mutate({ id: project.id, patch: body }, done);
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="lg"
      title={isCreate ? "Nouveau projet" : `Modifier ${project.slug}`}
      description={
        isCreate
          ? "Le projet est créé désactivé. Un projet product exigera une confirmation explicite pour être activé."
          : undefined
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Annuler
          </Button>
          <Button
            variant="primary"
            type="submit"
            form="project-form"
            loading={m.isPending}
          >
            {isCreate ? "Créer" : "Enregistrer"}
          </Button>
        </>
      }
    >
      <form
        id="project-form"
        onSubmit={submit}
        noValidate
        className="grid gap-4 sm:grid-cols-2"
      >
        {m.isError && (
          <p role="alert" className="text-sm text-danger sm:col-span-2">
            {errorMessage(m.error)}
          </p>
        )}
        <Field
          label="Slug"
          required
          error={err("slug")}
          hint={
            isCreate ? "Identifiant court, non modifiable." : "Non modifiable."
          }
        >
          {(p) => (
            <Input
              {...p}
              value={f.slug}
              onChange={(e) => set("slug", e.target.value.toLowerCase())}
              disabled={!isCreate}
              className="font-mono"
              data-autofocus
            />
          )}
        </Field>
        <Field label="Nom" required error={err("name")}>
          {(p) => (
            <Input
              {...p}
              value={f.name}
              onChange={(e) => set("name", e.target.value)}
            />
          )}
        </Field>
        <Field label="Dépôt GitHub" required error={err("repository")}>
          {(p) => (
            <Input
              {...p}
              value={f.repository}
              onChange={(e) => set("repository", e.target.value)}
              placeholder="owner/repo"
              className="font-mono"
            />
          )}
        </Field>
        <Field label="Branche de base" required error={err("base_branch")}>
          {(p) => (
            <Input
              {...p}
              value={f.base_branch}
              onChange={(e) => set("base_branch", e.target.value)}
              className="font-mono"
            />
          )}
        </Field>
        <Field
          label="Mode"
          hint={
            f.mode === "product"
              ? "Dépôt de production : activation protégée."
              : "Bac à sable pour expérimenter."
          }
        >
          {(p) => (
            <Select
              {...p}
              value={f.mode}
              onChange={(e) => set("mode", e.target.value as ProjectMode)}
            >
              <option value="sandbox">sandbox</option>
              <option value="product">product</option>
            </Select>
          )}
        </Field>
        <Field
          label="Intégration par défaut des features"
          hint={`${INTEGRATION_HINTS[f.integration_mode]} Modifiable sur chaque feature.`}
        >
          {(p) => (
            <Select
              {...p}
              value={f.integration_mode}
              onChange={(e) => set("integration_mode", e.target.value as IntegrationMode)}
            >
              <option value="direct">Directe</option>
              <option value="feature_branch">Branche de feature</option>
            </Select>
          )}
        </Field>
        <Field
          label="Commande de test"
          error={err("test_command")}
          hint="Indicative pour les agents."
        >
          {(p) => (
            <Input
              {...p}
              value={f.test_command}
              onChange={(e) => set("test_command", e.target.value)}
              className="font-mono"
              placeholder="go test ./..."
            />
          )}
        </Field>
        {PROFILE_ROLES.map(({ role, key, label }) => (
          <Field
            key={key}
            label={label}
            error={err(key)}
            hint="Profils Hermes dont la clé est configurée."
          >
            {(p) => (
              <ProfileSelect
                {...p}
                role={role}
                value={f[key]}
                onChange={(v) => set(key, v)}
                defaultProfile={defaults[role]}
                profiles={profiles.data ?? []}
              />
            )}
          </Field>
        ))}
        <AgentModelsFields
          values={f.models}
          onChange={(role, v) =>
            setF((s) => ({ ...s, models: { ...s.models, [role]: v } }))
          }
          providers={models.data ?? []}
          error={models.isError ? errorMessage(models.error) : undefined}
          loading={models.isLoading}
        />
        <Field
          label="Groupe Telegram"
          error={err("telegram_chat")}
          hint="Identifiant du groupe (vide = chat par défaut)."
        >
          {(p) => (
            <Input
              {...p}
              value={f.telegram_chat}
              onChange={(e) => set("telegram_chat", e.target.value)}
              className="font-mono"
              placeholder="-1001234567890"
            />
          )}
        </Field>
        <Field
          label="Sujet Telegram"
          error={err("telegram_thread")}
          hint="Numéro du sujet du forum (vide = aucun)."
        >
          {(p) => (
            <Input
              {...p}
              value={f.telegram_thread}
              onChange={(e) => set("telegram_thread", e.target.value)}
              className="font-mono"
              inputMode="numeric"
              placeholder="13"
            />
          )}
        </Field>
        <TelegramTest
          project={project}
          chat={f.telegram_chat}
          thread={f.telegram_thread}
        />
        <Field
          label="Checks CI requis"
          error={err("required_checks")}
          className="sm:col-span-2"
          hint="Un par ligne (ou séparés par des virgules). CI verte = chacun completed/success sur le SHA exact."
        >
          {(p) => (
            <textarea
              {...p}
              value={f.required_checks}
              onChange={(e) => set("required_checks", e.target.value)}
              rows={3}
              className="w-full rounded-md border border-border bg-panel px-3 py-2 font-mono text-sm text-fg focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
            />
          )}
        </Field>
      </form>
    </Dialog>
  );
}

export default function ProjectsPage() {
  const q = useProjects();
  const patch = usePatchProject();
  const toast = useToast();
  const [dialog, setDialog] = useState<{
    open: boolean;
    project: Project | null;
  }>({ open: false, project: null });
  const [toggle, setToggle] = useState<{ p: Project; enable: boolean } | null>(
    null,
  );

  const doToggle = () => {
    if (!toggle) return;
    const { p, enable } = toggle;
    const body =
      enable && p.mode === "product"
        ? { enabled: true, confirm: p.slug }
        : { enabled: enable };
    patch.mutate(
      { id: p.id, patch: body },
      {
        onSuccess: () => {
          toast.success(enable ? "Projet activé" : "Projet désactivé");
          setToggle(null);
        },
        onError: (e) => toast.error("Modification refusée", errorMessage(e)),
      },
    );
  };

  return (
    <>
      <PageHeader
        title="Projets"
        description="Dépôts GitHub pilotés par Agentic OS."
        actions={
          <Button
            variant="primary"
            onClick={() => setDialog({ open: true, project: null })}
            icon={<Plus className="h-4 w-4" aria-hidden />}
          >
            Nouveau projet
          </Button>
        }
      />
      <Card>
        <QueryView
          query={q}
          isEmpty={(d) => d.length === 0}
          empty={
            <EmptyState
              icon={<FolderGit2 className="h-5 w-5" />}
              title="Aucun projet"
              description="Ajoutez un dépôt, en mode sandbox pour commencer."
            />
          }
        >
          {(d) => (
            <div className="scroll-thin overflow-x-auto">
              <table className="w-full min-w-[760px] text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-xs text-fg-subtle">
                    <th className="px-4 py-2 font-medium">Projet</th>
                    <th className="px-4 py-2 font-medium">Dépôt</th>
                    <th className="px-4 py-2 font-medium">Mode</th>
                    <th className="px-4 py-2 font-medium">Checks requis</th>
                    <th className="px-4 py-2 font-medium">Créé</th>
                    <th className="px-4 py-2 font-medium">Actif</th>
                    <th className="px-4 py-2" />
                  </tr>
                </thead>
                <tbody>
                  {d.map((p) => (
                    <tr
                      key={p.id}
                      className="border-b border-border/60 last:border-0 hover:bg-hover/40"
                    >
                      <td className="px-4 py-2.5">
                        <p className="font-medium text-fg">{p.name}</p>
                        <p className="font-mono text-xs text-fg-subtle">
                          {p.slug}
                        </p>
                      </td>
                      <td className="px-4 py-2.5">
                        <Mono>{p.repository}</Mono>{" "}
                        <span className="text-xs text-fg-subtle">
                          ← {p.base_branch}
                        </span>
                      </td>
                      <td className="px-4 py-2.5">
                        {p.mode === "product" ? (
                          <ToneBadge tone="warning">
                            <ShieldAlert className="h-3 w-3" aria-hidden />
                            product
                          </ToneBadge>
                        ) : (
                          <ToneBadge tone="neutral">sandbox</ToneBadge>
                        )}
                      </td>
                      <td className="px-4 py-2.5">
                        <div className="flex flex-wrap gap-1">
                          {p.required_checks.length ? (
                            p.required_checks.map((c) => (
                              <Tag key={c} className="font-mono">
                                {c}
                              </Tag>
                            ))
                          ) : (
                            <span className="text-xs text-fg-subtle">
                              aucun
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-fg-subtle">
                        <Time value={p.created_at} />
                      </td>
                      <td className="px-4 py-2.5">
                        <Switch
                          checked={p.enabled}
                          label={`${p.enabled ? "Désactiver" : "Activer"} ${p.slug}`}
                          onChange={(v) => setToggle({ p, enable: v })}
                        />
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-right">
                        <Link
                          to={`/projects/${p.id}/git`}
                          className="focus-ring mr-1 inline-flex h-8 items-center gap-1.5 rounded-md px-2.5 text-sm text-fg-muted hover:bg-hover hover:text-fg"
                        >
                          <GitBranch className="h-3.5 w-3.5" aria-hidden />
                          Graphe Git
                        </Link>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setDialog({ open: true, project: p })}
                          icon={<Pencil className="h-3.5 w-3.5" aria-hidden />}
                        >
                          Modifier
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </QueryView>
      </Card>
      <ProjectDialog
        open={dialog.open}
        project={dialog.project}
        onClose={() => setDialog({ open: false, project: null })}
      />
      {toggle && (
        <ConfirmDialog
          open
          onClose={() => setToggle(null)}
          onConfirm={doToggle}
          loading={patch.isPending}
          title={
            toggle.enable
              ? `Activer ${toggle.p.slug} ?`
              : `Désactiver ${toggle.p.slug} ?`
          }
          description={
            toggle.enable
              ? toggle.p.mode === "product"
                ? `Projet en mode PRODUCT : les agents pourront créer des issues, pousser des branches et ouvrir des PR draft sur ${toggle.p.repository}.`
                : `Les agents pourront travailler sur ${toggle.p.repository}.`
              : "Le projet sera marqué inactif."
          }
          confirmLabel={toggle.enable ? "Activer" : "Désactiver"}
          tone={
            toggle.enable && toggle.p.mode === "product" ? "danger" : "primary"
          }
          requireText={
            toggle.enable && toggle.p.mode === "product"
              ? toggle.p.slug
              : undefined
          }
        />
      )}
    </>
  );
}
