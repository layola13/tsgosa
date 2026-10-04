class Base {
  greet(): i32 {
    return 100;
  }
}
class Sub extends Base {
  greet(): i32 {
    return super.greet() + 1;
  }
}
function main(): i32 {
  const s = new Sub();
  console.log(s.greet());
  return 0;
}
