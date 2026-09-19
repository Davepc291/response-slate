import { Component, inject } from '@angular/core';
import { Router } from '@angular/router';

@Component({
  selector: 'app-mobile-welcome',
  templateUrl: './mobile-welcome.html',
  styleUrl: './mobile-welcome.scss',
})
export class MobileWelcome {
  private readonly router = inject(Router);

  enterSyntheticPreview(): void {
    void this.router.navigateByUrl('/mobile/home');
  }
}
