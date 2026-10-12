function g(t: string|null): string {
  return t?.[0] ?? "?";
}
function main(): i32 {
  console.log(g("hey"));
  console.log(g(null));
  return 0;
}
