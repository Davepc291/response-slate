/**
 * Reads the current value out of a native `input`/`change` event's target.
 * Used instead of `@angular/forms`' `NgModel` so form fields stay plain,
 * signal-backed state (`[value]="x()"` + `(input)="x.set(inputValue($event))"`)
 * with no extra form-directive layer between the DOM and the signal.
 */
export function inputValue(event: Event): string {
  return (event.target as HTMLInputElement).value;
}
