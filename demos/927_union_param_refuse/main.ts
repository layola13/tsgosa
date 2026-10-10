function f(v: i32|string): i32 {
  if (typeof v === "string") { return 1; }
  return 0;
}
function main(): i32 {
  console.log(f(1));
  return 0;
}
