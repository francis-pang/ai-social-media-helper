/**
 * Cognito authentication for cloud mode (DDR-028 Problem 2).
 *
 * Uses amazon-cognito-identity-js for direct Cognito User Pool auth
 * without the full Amplify SDK. SPA-friendly: tokens stored in memory
 * and refreshed automatically.
 *
 * In local mode, this module is not used — no auth is required.
 */
import {
  CognitoUserPool,
  CognitoUser,
  AuthenticationDetails,
  CognitoUserSession,
} from "amazon-cognito-identity-js";
import { signal } from "@preact/signals";
import { isCloudMode } from "../api/client";

// Cognito User Pool configuration — injected at build time via Vite env vars.
// Supports multiple comma-separated pool/client ID pairs for resilience across
// redeployments. The first valid pair is used; on pool-not-found errors during
// sign-in, the next pair is tried automatically.
const POOL_IDS = (import.meta.env.VITE_COGNITO_USER_POOL_ID || "")
  .split(",")
  .map((s: string) => s.trim())
  .filter(Boolean);
const CLIENT_IDS = (import.meta.env.VITE_COGNITO_CLIENT_ID || "")
  .split(",")
  .map((s: string) => s.trim())
  .filter(Boolean);

interface PoolConfig {
  userPoolId: string;
  clientId: string;
}

/** Ordered list of pool/client pairs to try. */
const POOL_CONFIGS: PoolConfig[] = POOL_IDS.map(
  (id: string, i: number): PoolConfig => ({
    userPoolId: id,
    clientId: CLIENT_IDS[i] || "",
  }),
).filter((c: PoolConfig) => c.userPoolId && c.clientId);

/** Index of the currently active pool config. */
let activeConfigIndex = 0;

const userPools = new Map<number, CognitoUserPool>();

function getUserPool(configIndex?: number): CognitoUserPool {
  const idx = configIndex ?? activeConfigIndex;
  if (!userPools.has(idx)) {
    const cfg = POOL_CONFIGS[idx];
    userPools.set(
      idx,
      new CognitoUserPool({
        UserPoolId: cfg.userPoolId,
        ClientId: cfg.clientId,
      }),
    );
  }
  return userPools.get(idx)!;
}

/** Returns true if the error indicates the pool or client no longer exists. */
function isPoolNotFoundError(err: Error): boolean {
  const msg = err.message || "";
  return (
    msg.includes("does not exist") ||
    msg.includes("User pool client") ||
    msg.includes("ResourceNotFoundException")
  );
}

/** Whether the user is currently authenticated. */
export const isAuthenticated = signal<boolean>(false);

/** Error message from the last auth attempt. */
export const authError = signal<string | null>(null);

/** Whether auth is still being checked (e.g., refreshing tokens on page load). */
export const authLoading = signal<boolean>(true);

/**
 * Whether auth is required for API calls.
 * In local mode, auth is never required.
 */
export function isAuthRequired(): boolean {
  return isCloudMode && POOL_CONFIGS.length > 0;
}

/**
 * Get the current valid JWT ID token for API requests.
 * Checks each configured pool for a current user session.
 * Returns null if not authenticated or token cannot be refreshed.
 */
export function getIdToken(): Promise<string | null> {
  if (!isAuthRequired()) return Promise.resolve(null);

  // Try each pool config — the user session is stored per-pool in localStorage.
  function tryPool(idx: number): Promise<string | null> {
    if (idx >= POOL_CONFIGS.length) return Promise.resolve(null);

    return new Promise<string | null>((resolve) => {
      const pool = getUserPool(idx);
      const user = pool.getCurrentUser();
      if (!user) {
        resolve(tryPool(idx + 1));
        return;
      }

      user.getSession(
        (err: Error | null, session: CognitoUserSession | null) => {
          if (err || !session || !session.isValid()) {
            resolve(tryPool(idx + 1));
            return;
          }
          activeConfigIndex = idx;
          resolve(session.getIdToken().getJwtToken());
        },
      );
    });
  }

  return tryPool(0);
}

/**
 * Attempt to restore an existing session on page load.
 * Called once at app startup.
 */
export async function checkExistingSession(): Promise<void> {
  if (!isAuthRequired()) {
    isAuthenticated.value = true;
    authLoading.value = false;
    return;
  }

  try {
    const token = await getIdToken();
    isAuthenticated.value = token !== null;
  } catch {
    isAuthenticated.value = false;
  } finally {
    authLoading.value = false;
  }
}

/**
 * Sign in with email and password.
 * Tries each configured pool/client pair in order; if a pool-not-found error
 * occurs, automatically falls through to the next configuration.
 */
export function signIn(email: string, password: string): Promise<void> {
  authError.value = null;

  function tryConfig(configIndex: number): Promise<void> {
    if (configIndex >= POOL_CONFIGS.length) {
      const err = new Error(
        "Authentication failed: none of the configured Cognito pools are reachable.",
      );
      isAuthenticated.value = false;
      authError.value = err.message;
      return Promise.reject(err);
    }

    return new Promise<void>((resolve, reject) => {
      const pool = getUserPool(configIndex);
      const user = new CognitoUser({
        Username: email,
        Pool: pool,
      });

      const authDetails = new AuthenticationDetails({
        Username: email,
        Password: password,
      });

      user.authenticateUser(authDetails, {
        onSuccess: () => {
          activeConfigIndex = configIndex;
          isAuthenticated.value = true;
          authError.value = null;
          resolve();
        },
        onFailure: (err: Error) => {
          if (
            isPoolNotFoundError(err) &&
            configIndex + 1 < POOL_CONFIGS.length
          ) {
            // Pool/client no longer exists — try the next config.
            tryConfig(configIndex + 1).then(resolve, reject);
            return;
          }
          isAuthenticated.value = false;
          authError.value = err.message || "Authentication failed";
          reject(err);
        },
        newPasswordRequired: () => {
          isAuthenticated.value = false;
          authError.value =
            "Password change required. Please use the AWS CLI to set a permanent password.";
          reject(new Error("NEW_PASSWORD_REQUIRED"));
        },
      });
    });
  }

  return tryConfig(0);
}

/**
 * Sign out and clear tokens from all configured pools.
 */
export function signOut(): void {
  if (!isAuthRequired()) return;

  for (let i = 0; i < POOL_CONFIGS.length; i++) {
    const pool = getUserPool(i);
    const user = pool.getCurrentUser();
    if (user) {
      user.signOut();
    }
  }
  isAuthenticated.value = false;
}
