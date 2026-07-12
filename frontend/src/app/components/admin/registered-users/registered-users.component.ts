import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService, RegisteredUser } from '../../../services/admin.service';

@Component({
  selector: 'app-registered-users',
  imports: [
    NbCardModule,
    FormsModule,
  ],
  templateUrl: './registered-users.component.html',
  styleUrl: './registered-users.component.scss'
})
export class RegisteredUsersComponent implements OnInit {
  users: RegisteredUser[] = [];
  filteredUsers: RegisteredUser[] = [];
  searchTerm = '';
  isLoading = true;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.load();
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
