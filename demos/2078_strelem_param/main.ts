function cat(sa: string[], i: number, t: string): string {
  sa[i] += t;
  return sa[i];
}
function main(): i32 {
  const sa: string[] = ["a", "b"];
  console.log(cat(sa, 1, "?"));
  console.log(sa[1]);
  return 0;
}
