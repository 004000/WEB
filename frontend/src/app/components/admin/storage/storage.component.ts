import { Component, OnInit } from '@angular/core';
import { NbButtonModule, NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService, StorageUsage, CleanupResult } from '../../../services/admin.service';

@Component({
  selector: 'app-storage',
  imports: [
    NbCardModule,
    NbButtonModule,
  ],
  templateUrl: './storage.component.html',
  styleUrl: './storage.component.scss'
})
export class StorageComponent implements OnInit {
  usage: StorageUsage | null = null;
  isRunning = false;
  lastResult: CleanupResult | null = null;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.loadUsage();
  }

  loadUsage() {
    this.adminService.getStorageUsage()
      .then(usage => this.usage = usage)
      .catch(() => this.toastrService.danger('', 'שגיאה בטעינת נתוני האחסון'));
  }

  formatBytes(bytes: number): string {
    if (!bytes) return '0 MB';
    const mb = bytes / (1024 * 1024);
    return `${mb.toFixed(2)} MB`;
  }

  runCleanup() {
    this.isRunning = true;
    this.adminService.runCleanup()
      .then(result => {
        this.lastResult = result;
        if (result.messagesPurged > 0) {
          this.toastrService.success('', `נמחקו לצמיתות ${result.messagesPurged} הודעות ישנות`);
        } else if (result.backupSkipped) {
          this.toastrService.warning('', 'הגיבוי לא הוגדר, לא בוצעה מחיקה');
        } else {
          this.toastrService.success('', 'אין הודעות ישנות למחיקה כרגע');
        }
        this.loadUsage();
      })
      .catch(() => this.toastrService.danger('', 'הניקוי נכשל'))
      .finally(() => this.isRunning = false);
  }
}
