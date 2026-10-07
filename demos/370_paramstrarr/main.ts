function first(a: string[]): string {
  return a[0];
}
function main(): i32 {
  const a: string[] = ["x", "yy"];
  console.log(first(a));
  console.log(a[1]);
  return 0;
}
