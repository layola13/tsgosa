function eq0(a: number[], f: boolean): number {
  return f == a[0] ? 1 : 0;
}
function main(): i32 {
  console.log(eq0([5], true));
  return 0;
}
