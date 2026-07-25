import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('unauthenticated visit to /chat redirects to /login', async ({ page }) => {
	await page.goto('/chat');
	await expect(page).toHaveURL(/\/login/);
});

test('login navigates away from /login, and logout returns to it', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await expect(page).not.toHaveURL(/\/login/);

	await page.getByRole('button', { name: /log out/i }).click();
	await expect(page).toHaveURL(/\/login/);
});

test('serves install identity and shows branding on login, desktop, and mobile', async ({
	page,
	request
}) => {
	await page.goto('/login');
	const loginHeading = page.getByRole('heading', { name: 'Kadence' });
	await expect(loginHeading).toBeVisible();
	await expect(loginHeading.locator('img')).toHaveAttribute('alt', '');
	await expect(loginHeading.locator('img')).toHaveAttribute('width', '72');
	await expect(loginHeading.locator('img')).toHaveAttribute('height', '72');

	const manifestResponse = await request.get('/manifest.json');
	expect(manifestResponse.ok()).toBe(true);
	expect(manifestResponse.headers()['content-type']).toContain('application/json');
	expect(await manifestResponse.json()).toMatchObject({
		name: 'Kadence',
		short_name: 'Kadence',
		display: 'standalone'
	});

	for (const path of ['/favicon.png', '/icons/icon-192.png', '/icons/icon-maskable-512.png']) {
		const response = await request.get(path);
		expect(response.ok()).toBe(true);
		expect(response.headers()['content-type']).toContain('image/png');
	}

	await login(page, USERNAME, PASSWORD);
	const desktopBrand = page.getByRole('link', { name: 'Kadence' });
	await expect(desktopBrand).toBeVisible();
	await expect(desktopBrand.locator('img')).toHaveAttribute('alt', '');
	await expect(desktopBrand.locator('img')).toHaveAttribute('width', '24');
	await expect(desktopBrand.locator('img')).toHaveAttribute('height', '24');

	await page.setViewportSize({ width: 390, height: 844 });
	const mobileBrand = page.locator('.mobilebar .brand-sm');
	await expect(mobileBrand).toBeVisible();
	await expect(mobileBrand.locator('img')).toHaveAttribute('alt', '');
	await expect(mobileBrand.locator('img')).toHaveAttribute('width', '24');
	await expect(mobileBrand.locator('img')).toHaveAttribute('height', '24');
});
