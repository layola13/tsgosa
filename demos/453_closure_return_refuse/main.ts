function mk(): () => i32 {
  const c = 10;
  return (): i32 => {
    return c + 1;
  };
}
function main(): i32 {
  const f = mk();
  console.log(f());
  return 0;
}
