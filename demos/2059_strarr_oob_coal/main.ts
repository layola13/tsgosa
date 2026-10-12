function main(): i32 {
  const sa: string[] = ["a", "b"];
  console.log(sa[5] ?? "d");
  console.log(sa[0] ?? "d");
  return 0;
}
