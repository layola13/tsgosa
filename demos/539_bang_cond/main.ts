function main(): i32 {
  console.log(!"s" ? 1 : 0);
  console.log(!!"s" ? 1 : 0);
  console.log(!"" ? 1 : 0);
  console.log(!5 ? 1 : 0);
  console.log(!0 ? 1 : 0);
  const t = "x";
  console.log(!t ? 1 : 0);
  console.log(!!t ? 1 : 0);
  return 0;
}
