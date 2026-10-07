function next(): i32 {
  console.log(7);
  return 3;
}
function main(): i32 {
  const ok: i32 = Number.isSafeInteger(next());
  console.log(ok);
  return ok;
}
console.log(main());
