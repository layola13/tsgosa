let n: i32 = 0;
function bump(): string {
  n = n + 1;
  return "b";
}
function main(): i32 {
  const s: string | null = "a";
  console.log(s ?? bump());
  console.log(n);
  return 0;
}
