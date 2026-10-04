function cmp(a: string, b: string): i32 {
  const n: i32 = a.length;
  for (let i: i32 = 0; i < n; i++) {
    const d: i32 = a.charCodeAt(i) - b.charCodeAt(i);
    if (d != 0) {
      return d;
    }
  }
  return a.length - b.length;
}
function main(): i32 {
  console.log(cmp("abc", "abd"), cmp("abc", "abc"));
  return 0;
}