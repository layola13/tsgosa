function isPal(s: string): i32 {
  const n: i32 = s.length;
  for (let i: i32 = 0; i < n; i++) {
    if (s.charCodeAt(i) != s.charCodeAt(n - 1 - i)) {
      return 0;
    }
  }
  return 1;
}
function main(): i32 {
  console.log(isPal("abba"), isPal("abc"));
  return 0;
}
