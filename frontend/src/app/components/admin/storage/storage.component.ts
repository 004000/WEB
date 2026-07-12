import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { DecimalPipe } from '@angular/common';
import { NbButtonModule, NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService, StorageUsage, CleanupResult } from '../../../services/admin.service';

@Component({
  selector: 'app-storage',
  imports: [
    NbCardModule,
    NbButtonModule,
    FormsModule,
    DecimalPipe,
  ],
  templateUrl: './storage.component.html',
  styleUrl: './storage.component.scss'
})
export class StorageComponent implements OnInit {
  usage: StorageUsage | null = null;
  isRunning = false;
  isRunningEmergency = false;
  isSavingThreshold = false;
  lastResult: CleanupResult | null = null;

  thresholdOptions = [60, 70, 80, 90, 95];
  selectedThreshold = 80;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.loadUsage();
  }

  loadUsage() {
    this.adminService.getStorageUsage()
      .then(usage => {
        this.usage = usage;
        this.selectedThreshold = usage.emergencyThreshold;
      })
      .catch(() => this.toastrService.danger('', 'שגיאה בטעינת נתוני האחסון'));
  }

  formatBytes(bytes: number): string {
    if (!bytes) return '0 MB';
    const mb = bytes / (1024 * 1024);
    return `${mb.toFixed(2)} MB`;
  }

  saveThreshold() {
    this.isSavingThreshold = true;
    this.adminService.setEmergencyThreshold(this.selectedThreshold)
      .then(() => {
        this.toastrService.success('', `סף החירום עודכן ל-${this.selectedThreshold}%`);
        this.loadUsage();
      })
      .catch(() => this.toastrService.danger('', 'עדכון הסף נכשל'))
      .finally(() => this.isSavingThreshold = false);
  }

  private showResultToast(result: CleanupResult, emergencyLabel: boolean) {
    if (result.messagesPurged > 0) {
      this.toastrService.success('', `נמחקו לצמיתות ${result.messagesPurged} הודעות ${emergencyLabel ? 'ישנות (חירום)' : 'ישנות'}`);
    } else if (result.backupSkipped) {
      this.toastrService.warning('', 'הגיבוי לא הוגדר, לא בוצעה מחיקה');
    } else {
      this.toastrService.success('', 'אין הודעות למחיקה כרגע');
    }
  }

  runCleanup() {
    this.isRunning = true;
    this.adminService.runCleanup()
      .then(result => {
        this.lastResult = result;
        this.showResultToast(result, false);
        this.loadUsage();
      })
      .catch(() => this.toastrService.danger('', 'הניקוי נכשל'))
      .finally(() => this.isRunning = false);
  }

  runEmergencyCleanup() {
    this.isRunningEmergency = true;
    this.adminService.runEmergencyCleanup()
      .then(result => {
        this.lastResult = result;
        this.showResultToast(result, true);
        this.loadUsage();
      })
      .catch(() => this.toastrService.danger('', 'ניקוי החירום נכשל'))
      .finally(() => this.isRunningEmergency = false);
  }
}
