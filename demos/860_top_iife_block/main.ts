function main(): i32 {
  console.log((() => {
    const y = 4;
    return y * 2;
  })());
  return 0;
}
