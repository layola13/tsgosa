export function assertTrue(cond: number): void {
  if (cond == 0) {
    throw new Error("assertTrue failed");
  }
}
export function assertEq(actual: number, expected: number): void {
  if (actual != expected) {
    throw new Error("assertEq failed");
  }
}
export function assertNe(actual: number, unexpected: number): void {
  if (actual == unexpected) {
    throw new Error("assertNe failed");
  }
}
export function assertStrEq(actual: string, expected: string): void {
  if (actual != expected) {
    throw new Error("assertStrEq failed");
  }
}
