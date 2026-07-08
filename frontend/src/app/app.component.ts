import { Component, OnInit, OnDestroy } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { NbCardModule, NbLayoutModule, NbThemeService } from "@nebular/theme";
import { HttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';
import { NotificationsService } from './services/notifications.service';

@Component({
  selector: 'app-root',
  imports: [
    RouterOutlet,
    NbLayoutModule,
    NbCardModule,
],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss'
})
export class AppComponent implements OnInit, OnDestroy {

  updateAvailable = false;
  private serverVersion: string | null = null;
  private versionCheckInterval: any;

  constructor(
    private notificationsService: NotificationsService,
    private themeService: NbThemeService,
    private http: HttpClient,
  ) { }

  ngOnInit(): void {
    this.notificationsService.init();

    const isDarkMode = localStorage.getItem('darkMode') === '1';
    if (isDarkMode) {
      this.themeService.changeTheme('custom-dark');
    }

    this.startVersionCheck();
  }

  ngOnDestroy(): void {
    clearInterval(this.versionCheckInterval);
  }

  reloadPage() {
    window.location.reload();
  }

  private async startVersionCheck() {
    try {
      const res = await firstValueFrom(this.http.get<{ version: string }>('/api/version'));
      this.serverVersion = res.version;
    } catch {
      return;
    }

    this.versionCheckInterval = setInterval(async () => {
      try {
        const res = await firstValueFrom(this.http.get<{ version: string }>('/api/version'));
        if (this.serverVersion && res.version !== this.serverVersion) {
          this.updateAvailable = true;
          clearInterval(this.versionCheckInterval);
        }
      } catch {
        // Ignore transient network errors
      }
    }, 60000);
  }
}
