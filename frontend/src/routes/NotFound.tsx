import { Link } from "react-router-dom";

export function NotFound() {
  return (
    <section className="placeholder">
      <h1 className="placeholder__title">Nothing here</h1>
      <p className="placeholder__note">
        That route does not exist. <Link to="/">Back to the three lanes</Link>.
      </p>
    </section>
  );
}
