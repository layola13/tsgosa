export function main(): i32 {
  const fz = Object.freeze({ a: 1 });
  console.log(fz.a);
  return 0;
}
