import { Link } from "react-router-dom";

export function NotFound() {
  return (
    <section>
      <h1 className="font-display text-3xl leading-tight tracking-tight text-ink">
        Nothing here
      </h1>
      <p className="mt-1 font-sans text-sm text-graphite">
        That route does not exist.{" "}
        <Link className="text-ink underline underline-offset-2" to="/">
          Back to the three lanes
        </Link>
        .
      </p>
    </section>
  );
}
