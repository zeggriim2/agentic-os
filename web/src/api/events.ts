import { createContext, createElement, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { SSE_EVENT_TYPES, type SseEventType, type SsePayload } from "./types";

export type StreamStatus = "connecting" | "connected" | "disconnected";

type Listener = (type: SseEventType, payload: SsePayload) => void;
const listeners = new Set<Listener>();

/** Abonnement aux événements temps réel (ex. run.event pour le détail d'un run). */
export function subscribeEvents(fn: Listener): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

/** Clés de requêtes à invalider pour chaque type d'événement SSE (§9). */
export const INVALIDATION_MAP: Record<SseEventType, string[]> = {
  "job.updated": ["overview", "pipeline", "runs"],
  "run.updated": ["run", "runs", "overview", "feature", "ticket"],
  "run.event": [],
  "approval.updated": ["approval", "approvals", "overview", "pipeline", "feature", "ticket"],
  "feature.updated": ["feature", "features", "pipeline", "overview"],
  "ticket.updated": ["ticket", "feature", "pipeline", "overview"],
  "overview.invalidate": ["overview"],
};

export function applyInvalidation(qc: QueryClient, type: SseEventType): void {
  for (const k of INVALIDATION_MAP[type]) void qc.invalidateQueries({ queryKey: [k] });
}

export function backoffDelay(attempt: number): number {
  const base = Math.min(30_000, 1000 * 2 ** attempt);
  return Math.round(base * (0.8 + Math.random() * 0.4));
}

const StreamContext = createContext<StreamStatus>("disconnected");
export const useStreamStatus = () => useContext(StreamContext);

export function EventStreamProvider({ children, enabled }: { children: ReactNode; enabled: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const attempt = useRef(0);

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") {
      setStatus("disconnected");
      return;
    }
    let es: EventSource | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let closed = false;

    const connect = () => {
      if (closed) return;
      setStatus("connecting");
      es = new EventSource("/api/events", { withCredentials: true });
      es.onopen = () => {
        attempt.current = 0;
        setStatus("connected");
        // Des événements ont pu être émis avant l'ouverture du flux : après une coupure, mais aussi
        // au chargement, entre la première lecture des données et la connexion. On rafraîchit tout.
        void qc.invalidateQueries();
      };
      es.onerror = () => {
        es?.close();
        es = null;
        if (closed) return;
        setStatus("disconnected");
        timer = setTimeout(connect, backoffDelay(attempt.current++));
      };
      for (const type of SSE_EVENT_TYPES) {
        es.addEventListener(type, (ev) => {
          let payload: SsePayload = {};
          try {
            payload = JSON.parse((ev as MessageEvent<string>).data) as SsePayload;
          } catch {
            /* payload illisible : on invalide quand même */
          }
          applyInvalidation(qc, type);
          listeners.forEach((l) => l(type, payload));
        });
      }
    };
    connect();
    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      es?.close();
    };
  }, [enabled, qc]);

  return createElement(StreamContext.Provider, { value: status }, children);
}
