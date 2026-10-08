interface P {
  b?: i32;
}
interface Q {
  a: i32;
}
function main(): i32 {
  const [x = 5] = [];
  console.log(x);
  const [m = 5, n] = [1, 2];
  console.log(m + n);
  const { b = 5 }: P = {};
  console.log(b);
  const { a = 5 }: Q = { a: 1 };
  console.log(a);
  return 0;
}
