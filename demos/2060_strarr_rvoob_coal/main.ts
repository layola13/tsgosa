function getSA(): string[] { return ["x"]; }
function main(): i32 {
  console.log(getSA()[5] ?? "d");
  console.log(getSA()[0] ?? "d");
  return 0;
}
