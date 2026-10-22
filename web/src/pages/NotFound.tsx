import { Link } from "react-router-dom";
import { EmptyState } from "@/components/ui/States";

export default function NotFoundPage() {
  return (
    <EmptyState
      title="Page introuvable"
      description="Cette adresse ne correspond à aucun écran."
      action={
        <Link to="/" className="focus-ring rounded text-sm text-accent hover:underline">
          Retour à la vue d'ensemble
        </Link>
      }
    />
  );
}
