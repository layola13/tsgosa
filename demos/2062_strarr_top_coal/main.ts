const TA: string[] = ["p", "q"];
function main(): i32 {
  console.log(TA[7] ?? "d");
  console.log(TA[0] ?? "d");
  return 0;
}
