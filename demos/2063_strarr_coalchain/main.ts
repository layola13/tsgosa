function main(): i32 {
  const sa: string[] = ["a", "b"];
  console.log(sa[5] ?? sa[0]);
  console.log((sa[5] ?? "d").length);
  return 0;
}
