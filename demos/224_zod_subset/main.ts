import { isLower, isDigits, isEmailLike } from "./patterns";

export function compute(): number {
  return isLower("hello") * 1 + isLower("Hello123") * 2 + isDigits("abc123") * 4 + isDigits("456") * 8 + isEmailLike("a@b") * 16 + isEmailLike("nope") * 32;
}
