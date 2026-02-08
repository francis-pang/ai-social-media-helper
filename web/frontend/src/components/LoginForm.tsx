/**
 * Login form for cloud mode Cognito authentication (DDR-028).
 *
 * Shown when isCloudMode is true and the user is not yet authenticated.
 * Uses direct Cognito User Pool auth (email + password).
 */
import { signal } from "@preact/signals";
import { signIn, authError } from "../auth/cognito";

const email = signal("");
const password = signal("");
const submitting = signal(false);

async function handleSubmit(e: Event) {
  e.preventDefault();
  if (submitting.value) return;

  submitting.value = true;
  try {
    await signIn(email.value, password.value);
    // isAuthenticated signal is updated by signIn — app.tsx will re-render
  } catch {
    // Error is set in authError signal by signIn
  } finally {
    submitting.value = false;
  }
}

export function LoginForm() {
  return (
    <div
      style={{
        display: "flex",
        justifyContent: "center",
        alignItems: "center",
        minHeight: "60vh",
      }}
    >
      <div class="card" style={{ maxWidth: "400px", width: "100%" }}>
        <h2 style={{ marginBottom: "1.5rem", textAlign: "center" }}>Sign In</h2>

        <form onSubmit={handleSubmit}>
          <div style={{ marginBottom: "1rem" }}>
            <label
              htmlFor="email"
              style={{
                display: "block",
                fontSize: "0.875rem",
                marginBottom: "0.25rem",
                color: "var(--color-text-secondary)",
              }}
            >
              Email
            </label>
            <input
              id="email"
              type="email"
              value={email.value}
              onInput={(e) => {
                email.value = (e.target as HTMLInputElement).value;
              }}
              required
              autoComplete="email"
              style={{
                width: "100%",
                padding: "0.5rem 0.75rem",
                borderRadius: "var(--radius)",
                border: "1px solid var(--color-border)",
                background: "var(--color-bg)",
                color: "var(--color-text)",
                fontSize: "0.875rem",
                boxSizing: "border-box",
              }}
            />
          </div>

          <div style={{ marginBottom: "1.5rem" }}>
            <label
              htmlFor="password"
              style={{
                display: "block",
                fontSize: "0.875rem",
                marginBottom: "0.25rem",
                color: "var(--color-text-secondary)",
              }}
            >
              Password
            </label>
            <input
              id="password"
              type="password"
              value={password.value}
              onInput={(e) => {
                password.value = (e.target as HTMLInputElement).value;
              }}
              required
              autoComplete="current-password"
              style={{
                width: "100%",
                padding: "0.5rem 0.75rem",
                borderRadius: "var(--radius)",
                border: "1px solid var(--color-border)",
                background: "var(--color-bg)",
                color: "var(--color-text)",
                fontSize: "0.875rem",
                boxSizing: "border-box",
              }}
            />
          </div>

          {authError.value && (
            <div
              style={{
                color: "var(--color-danger)",
                fontSize: "0.875rem",
                marginBottom: "1rem",
                textAlign: "center",
              }}
            >
              {authError.value}
            </div>
          )}

          <button
            type="submit"
            class="primary"
            disabled={submitting.value}
            style={{ width: "100%" }}
          >
            {submitting.value ? "Signing in..." : "Sign In"}
          </button>
        </form>
      </div>
    </div>
  );
}
