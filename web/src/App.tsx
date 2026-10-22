import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { lazy, Suspense, type ReactNode } from "react";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { ApiError } from "@/api/client";
import { FullScreenLoader, RequireAuth } from "@/auth/auth";
import { AppShell } from "@/components/layout/AppShell";
import { ToastProvider } from "@/components/ui/Toast";
import { ListSkeleton } from "@/components/ui/States";
import LoginPage from "@/pages/Login";
import OverviewPage from "@/pages/Overview";

const PipelinePage = lazy(() => import("@/pages/Pipeline"));
const FeaturesPage = lazy(() => import("@/pages/Features"));
const FeatureDetailPage = lazy(() => import("@/pages/FeatureDetail"));
const TicketDetailPage = lazy(() => import("@/pages/TicketDetail"));
const RunsPage = lazy(() => import("@/pages/Runs"));
const RunDetailPage = lazy(() => import("@/pages/RunDetail"));
const ApprovalsPage = lazy(() => import("@/pages/Approvals"));
const ApprovalDetailPage = lazy(() => import("@/pages/ApprovalDetail"));
const AgentsPage = lazy(() => import("@/pages/Agents"));
const ProjectsPage = lazy(() => import("@/pages/Projects"));
const AuditPage = lazy(() => import("@/pages/Audit"));
const HealthPage = lazy(() => import("@/pages/Health"));
const NotFoundPage = lazy(() => import("@/pages/NotFound"));

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 15_000,
        refetchOnWindowFocus: true,
        // Pas de nouvel essai sur les erreurs client (4xx) : inutile et bruyant.
        retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
      },
      mutations: { retry: false },
    },
  });
}

const page = (el: ReactNode) => <Suspense fallback={<ListSkeleton rows={6} />}>{el}</Suspense>;

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route index element={<OverviewPage />} />
        <Route path="pipeline" element={page(<PipelinePage />)} />
        <Route path="features" element={page(<FeaturesPage />)} />
        <Route path="features/:id" element={page(<FeatureDetailPage />)} />
        <Route path="tickets/:id" element={page(<TicketDetailPage />)} />
        <Route path="runs" element={page(<RunsPage />)} />
        <Route path="runs/:id" element={page(<RunDetailPage />)} />
        <Route path="approvals" element={page(<ApprovalsPage />)} />
        <Route path="approvals/:id" element={page(<ApprovalDetailPage />)} />
        <Route path="agents" element={page(<AgentsPage />)} />
        <Route path="projects" element={page(<ProjectsPage />)} />
        <Route path="audit" element={page(<AuditPage />)} />
        <Route path="health" element={page(<HealthPage />)} />
        <Route path="*" element={page(<NotFoundPage />)} />
      </Route>
    </Routes>
  );
}

export default function App({ client }: { client: QueryClient }) {
  return (
    <QueryClientProvider client={client}>
      <ToastProvider>
        <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <Suspense fallback={<FullScreenLoader />}>
            <AppRoutes />
          </Suspense>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  );
}
