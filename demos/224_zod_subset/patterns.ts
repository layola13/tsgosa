export function isLower(s: string): i32 {
  const re = /^[a-z]+$/;
  if (re.test(s)) {
    return 1;
  }
  return 0;
}
export function isDigits(s: string): i32 {
  const re2: RegExp = new RegExp("^[0-9]+$");
  if (re2.test(s)) {
    return 1;
  }
  return 0;
}
export function isEmailLike(s: string): i32 {
  const re3 = /^[^@]+@[^@]+$/;
  if (re3.test(s)) {
    return 1;
  }
  return 0;
}
