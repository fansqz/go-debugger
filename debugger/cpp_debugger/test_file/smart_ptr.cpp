#include <memory>
#include <string>
#include <iostream>

class Person {
public:
    std::string name;
    int age;
    std::unique_ptr<Person> friend_ptr;

    Person(const std::string& n, int a) : name(n), age(a) {}
};

int main() {
    // 创建智能指针
    auto person1 = std::make_unique<Person>("Alice", 25);
    auto person2 = std::make_unique<Person>("Bob", 30);
    
    // 设置朋友关系
    person1->friend_ptr = std::move(person2);
    
    // 创建一个普通指针作为对比
    Person* raw_ptr = new Person("Charlie", 35);
    
    // 断点位置
    std::cout << "Debug point" << std::endl;
    
    delete raw_ptr;
    return 0;
} 