import { Component, OnInit } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { NbCardModule, NbLayoutModule, NbThemeService } from "@nebular/theme";
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
export class AppComponent implements OnInit {

  constructor(
    private notificationsService: NotificationsService,
    private themeService: NbThemeService,
  ) { }

  ngOnInit(): void {
    this.notificationsService.init();

    const isDarkMode = localStorage.getItem('darkMode') === '1';
    if (isDarkMode) {
      this.themeService.changeTheme('custom-dark');
    }
  }
}
