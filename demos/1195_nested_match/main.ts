function first(s: string): string {
  return s.match(/[a-z]+/)[0] ?? "none";
}
function main(): i32 {
  console.log(first("abc123"));
  console.log(first("123"));
  return 0;
}
