import { Pipe, PipeTransform } from '@angular/core';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';

@Pipe({
  name: 'highlight'
})
export class HighlightPipe implements PipeTransform {

  constructor(private sanitizer: DomSanitizer) { }

  transform(value: string | undefined, query: string | undefined): SafeHtml {
    const text = value || '';
    const escaped = this.escapeHtml(text);

    if (!query || query.trim().length < 2) {
      return this.sanitizer.bypassSecurityTrustHtml(escaped);
    }

    const escapedQuery = this.escapeHtml(query.trim());
    const escapedRegexQuery = escapedQuery.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const regex = new RegExp(`(${escapedRegexQuery})`, 'gi');
    const highlighted = escaped.replace(regex, '<mark>$1</mark>');

    return this.sanitizer.bypassSecurityTrustHtml(highlighted);
  }

  private escapeHtml(text: string): string {
    return text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }
}
