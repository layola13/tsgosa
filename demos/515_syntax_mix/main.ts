function add(a: i32, b: i32): i32 {
  return a + b;
}
enum E {
  A,
  B,
}
function main(): i32 {
  let s = 0;
  for (const x of [1, 2, 3]) {
    s += x;
  }
  console.log(s);
  const [a, b] = [10, 20];
  console.log(a);
  console.log(b);
  const args = [3, 4];
  console.log(add(...args));
  console.log(E.A);
  return 0;
}
