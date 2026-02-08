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
// Set VITE_COGNITO_USER_POOL_ID and VITE_COGNITO_CLIENT_ID in the build environment.
const USER_POOL_ID = import.meta.env.VITE_COGNITO_USER_POOL_ID || "";
const CLIENT_ID = import.meta.env.VITE_COGNITO_CLIENT_ID || "";

let userPool: CognitoUserPool | null = null;

function getUserPool(): CognitoUserPool {
  if (!userPool) {
    userPool = new CognitoUserPool({
      UserPoolId: USER_POOL_ID,
      ClientId: CLIENT_ID,
    });
  }
  return userPool;
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
  return isCloudMode && USER_POOL_ID !== "" && CLIENT_ID !== "";
}

/**
 * Get the current valid JWT ID token for API requests.
 * Returns null if not authenticated or token cannot be refreshed.
 */
export function getIdToken(): Promise<string | null> {
  if (!isAuthRequired()) return Promise.resolve(null);

  return new Promise((resolve) => {
    const pool = getUserPool();
    const user = pool.getCurrentUser();
    if (!user) {
      resolve(null);
      return;
    }

    user.getSession(
      (err: Error | null, session: CognitoUserSession | null) => {
        if (err || !session || !session.isValid()) {
          resolve(null);
          return;
        }
        resolve(session.getIdToken().getJwtToken());
      },
    );
  });
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
 */
export function signIn(email: string, password: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const pool = getUserPool();
    const user = new CognitoUser({
      Username: email,
      Pool: pool,
    });

    const authDetails = new AuthenticationDetails({
      Username: email,
      Password: password,
    });

    authError.value = null;

    user.authenticateUser(authDetails, {
      onSuccess: () => {
        isAuthenticated.value = true;
        authError.value = null;
        resolve();
      },
      onFailure: (err: Error) => {
        isAuthenticated.value = false;
        authError.value = err.message || "Authentication failed";
        reject(err);
      },
      newPasswordRequired: () => {
        // First-time login after admin-create-user requires a password change.
        // For simplicity, we'll handle this in the login form.
        isAuthenticated.value = false;
        authError.value =
          "Password change required. Please use the AWS CLI to set a permanent password.";
        reject(new Error("NEW_PASSWORD_REQUIRED"));
      },
    });
  });
}

/**
 * Sign out and clear tokens.
 */
export function signOut(): void {
  if (!isAuthRequired()) return;

  const pool = getUserPool();
  const user = pool.getCurrentUser();
  if (user) {
    user.signOut();
  }
  isAuthenticated.value = false;
}
