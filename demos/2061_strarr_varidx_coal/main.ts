function main(): i32 {
  const sa: string[] = ["a", "b"];
  const i: number = 9;
  console.log(sa[i] ?? "d");
  console.log(sa[1] ?? "d");
  return 0;
}
