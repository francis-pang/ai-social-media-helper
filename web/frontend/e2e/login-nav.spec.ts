/**
 * E2E tests for login flow and navigation (Tests 1.1–1.6).
 * Run with: npx playwright test e2e/login-nav.spec.ts
 */
import { test, expect } from "@playwright/test";

const APP_URL = "https://d16bzg5fg3lwy5.cloudfront.net";
const EMAIL = "boyshawn@hotmail.com";
const PASSWORD = "!dcA7@i@L!6mKm%WrqP9#MB&0S9d*k1@";

test.describe("Login and Navigation E2E", () => {
  test("1.1 Login page renders - email input, password input, Sign In button", async ({
    page,
  }) => {
    await page.goto(APP_URL);
    await expect(page.getByRole("textbox", { name: /email/i })).toBeVisible();
    await expect(page.getByLabel(/password/i)).toBeVisible();
    await expect(page.getByRole("button", { name: /sign in/i })).toBeVisible();
  });

  test("1.2 Login success - landing page with Media Triage and Media Selection cards", async ({
    page,
  }) => {
    await page.goto(APP_URL);
    await page.getByRole("textbox", { name: /email/i }).fill(EMAIL);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole("button", { name: /sign in/i }).click();
    await page.waitForTimeout(2500);
    await expect(page.getByText("Media Triage")).toBeVisible();
    await expect(page.getByText("Media Selection")).toBeVisible();
  });

  test("1.3 Triage page navigation - file uploader with drop zone", async ({
    page,
  }) => {
    await page.goto(APP_URL);
    await page.getByRole("textbox", { name: /email/i }).fill(EMAIL);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole("button", { name: /sign in/i }).click();
    await page.waitForTimeout(2500);
    await page.getByRole("button", { name: /media triage/i }).click();
    await page.waitForTimeout(1000);
    await expect(
      page.getByText('Drop files here')
    ).toBeVisible();
  });

  test("1.4 Home navigation - back to landing with both workflow cards", async ({
    page,
  }) => {
    await page.goto(APP_URL);
    await page.getByRole("textbox", { name: /email/i }).fill(EMAIL);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole("button", { name: /sign in/i }).click();
    await page.waitForTimeout(2500);
    await page.getByRole("button", { name: /media triage/i }).click();
    await page.waitForTimeout(1000);
    await page.getByRole("button", { name: /^home$/i }).click();
    await page.waitForTimeout(1000);
    await expect(page.getByText("Media Triage")).toBeVisible();
    await expect(page.getByText("Media Selection")).toBeVisible();
  });

  test("1.5 Selection page navigation - file upload with Choose Files, trip context", async ({
    page,
  }) => {
    await page.goto(APP_URL);
    await page.getByRole("textbox", { name: /email/i }).fill(EMAIL);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole("button", { name: /sign in/i }).click();
    await page.waitForTimeout(2500);
    await page.getByRole("button", { name: /media selection/i }).click();
    await page.waitForTimeout(1000);
    await expect(
      page.getByRole("button", { name: /choose files/i })
    ).toBeVisible();
    await expect(
      page.getByPlaceholder(/trip|event|tokyo/i)
    ).toBeVisible();
  });

  test("1.6 Home navigation again - landing page with both workflow cards", async ({
    page,
  }) => {
    await page.goto(APP_URL);
    await page.getByRole("textbox", { name: /email/i }).fill(EMAIL);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole("button", { name: /sign in/i }).click();
    await page.waitForTimeout(2500);
    await page.getByRole("button", { name: /media selection/i }).click();
    await page.waitForTimeout(1000);
    await page.getByRole("button", { name: /^home$/i }).click();
    await page.waitForTimeout(1000);
    await expect(page.getByText("Media Triage")).toBeVisible();
    await expect(page.getByText("Media Selection")).toBeVisible();
  });
});
