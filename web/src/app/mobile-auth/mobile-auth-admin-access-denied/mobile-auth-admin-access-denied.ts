import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';

/**
 * Safe access-denied screen for the /mobile/auth/admin/** preview screens
 * (Step 9E requirement 7: "Unauthorized users receive a safe access-denied
 * screen or redirect"). Reached only through requireAdminRole's redirect;
 * it carries no user data of any kind and is safe to render for any
 * signed-in, non-administrator account.
 */
@Component({
  selector: 'app-mobile-auth-admin-access-denied',
  imports: [RouterLink],
  templateUrl: './mobile-auth-admin-access-denied.html',
  styleUrl: './mobile-auth-admin-access-denied.scss',
})
export class MobileAuthAdminAccessDenied {
  readonly headerTitle = HEADER_TITLE;
}
