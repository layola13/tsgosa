function pick(a: string | null): string {
  return a ?? "d";
}
function main(): i32 {
  console.log(pick(null) == "d" ? 1 : 0);
  console.log(pick("x") == "x" ? 1 : 0);
  return 0;
}
