function f(a: i32[]): i32 {
  if (Array.isArray(a)) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log(f([1]));
  const t = [1];
  console.log(typeof t == "object" ? 1 : 0);
  const x: i32 | null = null;
  console.log(x ?? 5 ?? 6);
  return 0;
}
