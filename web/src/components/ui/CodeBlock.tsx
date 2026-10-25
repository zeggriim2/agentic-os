import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/cn";

export function prettyJson(v: unknown): string {
  if (v === undefined) return "";
  if (typeof v === "string") {
    try {
      return JSON.stringify(JSON.parse(v), null, 2);
    } catch {
      return v;
    }
  }
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

/** Bloc de code JSON/texte formaté, avec copie dans le presse-papiers. */
export function CodeBlock({ value, json = true, className, maxHeight = "max-h-[480px]" }: { value: unknown; json?: boolean; className?: string; maxHeight?: string }) {
  const text = json ? prettyJson(value) : String(value ?? "");
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* presse-papiers indisponible (HTTP non sécurisé) */
    }
  };
  return (
    <div className={cn("group relative", className)}>
      {/* tabIndex : zone défilante atteignable au clavier (WCAG 2.1.1) */}
      <pre tabIndex={0} className={cn("focus-ring scroll-thin overflow-auto rounded-md border border-border bg-panel p-3 font-mono text-xs leading-relaxed text-fg", maxHeight)}>
        {text || <span className="text-fg-subtle">(vide)</span>}
      </pre>
      {text && (
        <button
          type="button"
          onClick={copy}
          className="focus-ring absolute right-2 top-2 rounded border border-border bg-elevated p-1 text-fg-subtle opacity-0 transition-opacity hover:text-fg focus-visible:opacity-100 group-hover:opacity-100"
          aria-label={copied ? "Copié" : "Copier"}
        >
          {copied ? <Check className="h-3.5 w-3.5" aria-hidden /> : <Copy className="h-3.5 w-3.5" aria-hidden />}
        </button>
      )}
    </div>
  );
}
