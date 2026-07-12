import { Component, EventEmitter, HostListener, Input, OnInit, OnDestroy, Output } from '@angular/core';

import {
  NbButtonModule,
  NbContextMenuModule, NbDialogService,
  NbIconModule,
  NbMenuItem,
  NbMenuService,
  NbThemeService,
  NbToastrService,
  NbUserModule
} from "@nebular/theme";
import { filter } from "rxjs";
import Viewer from 'viewerjs';
import { Router } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { AuthService } from '../../../services/auth.service';
import { ChatService } from '../../../services/chat.service';
import { NotificationsService } from '../../../services/notifications.service';
import { AdminPanelComponent } from "../../admin/admin-panel.component";
import { User } from '../../../models/user.model';
import { FormsModule } from '@angular/forms';
import { MessageTimePipe } from '../../../pipes/message-time.pipe';
import { HighlightPipe } from '../../../pipes/highlight.pipe';

@Component({
  selector: 'app-channel-header',
  imports: [
    NbButtonModule,
    NbIconModule,
    NbUserModule,
    NbContextMenuModule,
    FormsModule,
    MessageTimePipe,
    HighlightPipe
],
  templateUrl: './channel-header.component.html',
  styleUrl: './channel-header.component.scss'
})
export class ChannelHeaderComponent implements OnInit, OnDestroy {

  @Input()
  set userInfo(user: User | undefined) {
    this._userInfo = user;
    this.userMenu = [
      ...((user?.privileges?.['admin'] || user?.privileges?.['moderator']) ? [{
        title: 'ניהול ערוץ',
        icon: 'people-outline',
      }] : []),
      {
        title: 'התנתק',
        icon: 'log-out',
      }
    ];
  }

  get userInfo() {
    return this._userInfo;
  }

  private _userInfo?: User;

  @Output()
  userInfoChange: EventEmitter<User> = new EventEmitter<User>();

  userMenuTag = 'user-menu';
  userMenu: NbMenuItem[] = [];
  isSmallScreen = false;
  isDarkMode = false;
  isLargeText = false;

  displayedConnectedUsers: number | null = null;
  private connectedUsersAnimInterval: any;

  constructor(
    public chatService: ChatService,
    public _authService: AuthService,
    private contextMenuService: NbMenuService,
    private toastrService: NbToastrService,
    private router: Router,
    public notificationsService: NotificationsService,
    private titleService: Title,
    private dialogService: NbDialogService,
    private themeService: NbThemeService,
  ) {
    this.isDarkMode = localStorage.getItem('darkMode') === '1';
    this.isLargeText = localStorage.getItem('largeText') === '1';
    if (this.isLargeText) {
      document.documentElement.classList.add('a11y-large-text');
    }
  }

  @HostListener('window:resize')
  onResize() {
    this.updateScreenSize();
  }

  ngOnInit() {
    this.chatService.updateChannelInfo()
      .then(() => this.titleService.setTitle(this.chatService.channelInfo?.name || 'TheChannel'));

    this.startConnectedUsersAnimation();

    this.contextMenuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.userMenuTag))
      .subscribe(value => {
        switch (value.item.icon) {
          case 'log-out':
            this.logout();
            break;
          case 'people-outline':
            this.dialogService.open(AdminPanelComponent, { closeOnBackdropClick: true });
            break;
        }
      });

    this.updateScreenSize();
  }

  async logout() {
    if (await this._authService.logout()) {
      this.userInfo = undefined;
      this.userInfoChange.emit(undefined);
      try {
        await this._authService.loadUserInfo();
      } catch (err: any) {
        if (err.status === 401) {
          this.router.navigate(['/login']);
        }
      }

      const path = this.router.url;
      if (path !== '/') {
        this.router.navigate(['/']);
      }

    } else {
      this.toastrService.danger("", "שגיאה בהתנתקות");
    }
  }

  private v!: Viewer;

  viewLargeImage(event: MouseEvent) {
    const target = event.target as HTMLImageElement;
    if (target.tagName === 'IMG') {
      if (!this.v) {
        this.v = new Viewer(target, {
          toolbar: false,
          transition: true,
          navbar: false,
          title: false
        });
      }
      this.v.show();
    }
  }

  updateScreenSize() {
    this.isSmallScreen = window.innerWidth < 768;
  }

  openContactUs() {
    window.open(this.chatService.channelInfo?.contact_us, '_blank');
  }

  toggleDarkMode() {
    this.isDarkMode = !this.isDarkMode;
    this.themeService.changeTheme(this.isDarkMode ? 'custom-dark' : 'custom');
    localStorage.setItem('darkMode', this.isDarkMode ? '1' : '0');
  }

  toggleLargeText() {
    this.isLargeText = !this.isLargeText;
    document.documentElement.classList.toggle('a11y-large-text', this.isLargeText);
    localStorage.setItem('largeText', this.isLargeText ? '1' : '0');
  }

  private startConnectedUsersAnimation() {
    this.connectedUsersAnimInterval = setInterval(() => {
      const target = this.chatService.channelInfo?.connectedUsersAmount;
      if (target === undefined) {
        return;
      }
      if (this.displayedConnectedUsers === null) {
        this.displayedConnectedUsers = target;
        return;
      }
      if (this.displayedConnectedUsers === target) {
        return;
      }
      const diff = target - this.displayedConnectedUsers;
      const step = diff > 0 ? Math.max(1, Math.round(diff / 4)) : Math.min(-1, Math.round(diff / 4));
      this.displayedConnectedUsers += step;
      if ((step > 0 && this.displayedConnectedUsers > target) || (step < 0 && this.displayedConnectedUsers < target)) {
        this.displayedConnectedUsers = target;
      }
    }, 120);
  }

  ngOnDestroy(): void {
    clearInterval(this.connectedUsersAnimInterval);
  }
}
