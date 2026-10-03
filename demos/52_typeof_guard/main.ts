function f(v: unknown): i32 {
  if (typeof v === "undefined") {
    return 0;
  }
  return 1;
}
function main(): i32 {
  console.log(f(5));
  return 0;
}
