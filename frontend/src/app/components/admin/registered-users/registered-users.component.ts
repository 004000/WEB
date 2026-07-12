import { Component, OnDestroy, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService, RegisteredUser, LiveConnection } from '../../../services/admin.service';

@Component({
  selector: 'app-registered-users',
  imports: [
    NbCardModule,
    FormsModule,
  ],
  templateUrl: './registered-users.component.html',
  styleUrl: './registered-users.component.scss'
})
export class RegisteredUsersComponent implements OnInit, OnDestroy {
  activeTab: 'live' | 'all' = 'live';

  users: RegisteredUser[] = [];
  filteredUsers: RegisteredUser[] = [];
  searchTerm = '';
  isLoading = true;

  liveConnections: LiveConnection[] = [];
  isLoadingLive = true;
  private liveRefreshInterval: any;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.load();
    this.loadLive();
    this.liveRefreshInterval = setInterval(() => this.loadLive(), 15000);
  }

  ngOnDestroy(): void {
    clearInterval(this.liveRefreshInterval);
  }

  load() {
    this.isLoading = true;
    this.adminService.getRegisteredUsers()
      .then(users => {
        this.users = users;
        this.applyFilter();
      })
      .catch(() => this.toastrService.danger('', 'שגיאה בטעינת רשימת המשתמשים'))
      .finally(() => this.isLoading = false);
  }

  loadLive() {
    this.adminService.getConnectedUsersLive()
      .then(connections => this.liveConnections = connections)
      .catch(() => { })
      .finally(() => this.isLoadingLive = false);
  }

  applyFilter() {
    const term = this.searchTerm.trim().toLowerCase();
    this.filteredUsers = !term
      ? this.users
      : this.users.filter(u =>
        u.email.toLowerCase().includes(term) || u.name.toLowerCase().includes(term)
      );
  }

  formatDate(iso: string): string {
    if (!iso) return '-';
    const d = new Date(iso);
    return d.toLocaleString('he-IL');
  }
}
