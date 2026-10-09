function f(s: string | null): i32 {
  if (s === null) {
    return 0;
  }
  console.log(s);
  return 1;
}
function g(s: string | null): i32 {
  if (s !== null) {
    console.log(s);
    return 1;
  }
  return 0;
}
function h(s: string | undefined): i32 {
  if (null === s) {
    return 0;
  }
  if (s === undefined) {
    return 0;
  }
  console.log(s);
  return 1;
}
function main(): i32 {
  console.log(f(null));
  console.log(f("a"));
  console.log(g(null));
  console.log(g("b"));
  console.log(h(undefined));
  console.log(h("c"));
  return 0;
}
