function main(): i32 {
  let s: string | null = "hi";
  console.log(s ?? "empty");
  s = null;
  console.log(s ?? "empty");
  return 0;
}
