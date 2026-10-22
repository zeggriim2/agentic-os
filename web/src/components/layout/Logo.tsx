import { cn } from "@/lib/cn";

export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={cn("shrink-0", className)}>
      <rect width="32" height="32" rx="7" className="fill-accent-solid" />
      <path d="M9 22 16 9l7 13" fill="none" stroke="#fff" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="16" cy="18" r="2" fill="#fff" />
    </svg>
  );
}
