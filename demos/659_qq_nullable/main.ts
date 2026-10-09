function f(x: string | null): i32 {
  if (x ?? "d") {
    return 1;
  }
  return 0;
}
console.log(f(null));
console.log(f(""));
console.log(f("a"));
